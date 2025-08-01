/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mongodbv1 "github.com/amazeeio/dbaas-operator/apis/mongodb/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// MongoDBConsumerReconciler reconciles a MongoDBConsumer object
type MongoDBConsumerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// MongoDBProviderInfo .
type MongoDBProviderInfo struct {
	Hostname  string
	Username  string
	Password  string
	Port      string
	Name      string
	Namespace string
	Auth      mongodbv1.MongoDBAuth
	Type      string
}

// MongoDBConsumerInfo .
type MongoDBConsumerInfo struct {
	DatabaseName string
	Username     string
	Password     string
	Auth         mongodbv1.MongoDBAuth
}

// MongoDBUsage .
type MongoDBUsage struct {
	SchemaCount int
	TableCount  int
}

const (
	// LabelAppName for discovery.
	LabelAppName = "mongodb.amazee.io/service-name"
	// LabelAppType for discovery.
	LabelAppType = "mongodb.amazee.io/type"
	// LabelAppManaged for discovery.
	LabelAppManaged = "mongodb.amazee.io/managed-by"
)

// +kubebuilder:rbac:groups=mongodb.amazee.io,resources=mongodbconsumers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.amazee.io,resources=mongodbproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=mongodb.amazee.io,resources=mongodbconsumers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=list;get;watch;create;update;patch;delete

// Reconcile .
func (r *MongoDBConsumerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// ctx := context.Background()
	opLog := r.Log.WithValues("mongodbconsumer", req.NamespacedName)

	var mongodbConsumer mongodbv1.MongoDBConsumer
	if err := r.Get(ctx, req.NamespacedName, &mongodbConsumer); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.consumer.mongodb.amazee.io/v1"

	labels := map[string]string{
		LabelAppName:    mongodbConsumer.ObjectMeta.Name,
		LabelAppType:    "mongodbconsumer",
		LabelAppManaged: "dbaas-operator",
	}
	// check if the consumer is in a failed state, this prevents it from constantly re-trying to provision if its failed.
	// it also don't be able to be removed if it is in a failed state, as there could be an actual reason
	// and some human intervention required to prevent possible data loss
	skip := "false"
	if val, ok := mongodbConsumer.ObjectMeta.Annotations["dbaas.amazee.io/failed"]; !ok {
		skip = val
	}
	if skip != "true" {
		// examine DeletionTimestamp to determine if object is under deletion
		if mongodbConsumer.ObjectMeta.DeletionTimestamp.IsZero() {
			// set up the new credentials, use shorter names for database and username
			if mongodbConsumer.Spec.Consumer.Database == "" {
				mongodbConsumer.Spec.Consumer.Database = truncateString(req.NamespacedName.Namespace, 15) + "_" + randSeq(5, false)
			}
			if mongodbConsumer.Spec.Consumer.Username == "" {
				mongodbConsumer.Spec.Consumer.Username = truncateString(req.NamespacedName.Namespace, 15) + "_" + randSeq(5, false)
			}
			if mongodbConsumer.Spec.Consumer.Password == "" {
				mongodbConsumer.Spec.Consumer.Password = randSeq(20, false)
			}
			if mongodbConsumer.Spec.Consumer.Services.Primary == "" {
				mongodbConsumer.Spec.Consumer.Services.Primary = truncateString(req.Name, 25) + "-" + uuid.New().String()
			}

			provider := &MongoDBProviderInfo{}
			// if we haven't got any provider specific information pre-defined, we should query the providers to get one
			if mongodbConsumer.Spec.Provider.Hostname == "" || mongodbConsumer.Spec.Provider.Port == "" {
				opLog.Info(fmt.Sprintf("Attempting to create database %s on any usable mongodb provider", mongodbConsumer.Spec.Consumer.Database))
				// check the providers we have to see who is busy
				if err := r.checkMongoDBProviders(provider, &mongodbConsumer, req.NamespacedName); err != nil {
					opLog.Info("Error checking the providers in the cluster.")
					if patchErr := r.patchFailureStatus(ctx, &mongodbConsumer, fmt.Sprintf("error checking the providers in the cluster: %v", err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("unable to patch the mongodbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}
				if provider.Hostname == "" {
					opLog.Info("No suitable mongodb providers found, bailing")
					return ctrl.Result{}, nil
				}

				consumer := MongoDBConsumerInfo{
					DatabaseName: mongodbConsumer.Spec.Consumer.Database,
					Username:     mongodbConsumer.Spec.Consumer.Username,
					Password:     mongodbConsumer.Spec.Consumer.Password,
				}

				if err := createDatabaseIfNotExist(*provider, consumer); err != nil {
					opLog.Info("unable to create database")
					if patchErr := r.patchFailureStatus(ctx, &mongodbConsumer, fmt.Sprintf("unable to create database %v", err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("unable to patch the mongodbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}
				mongodbConsumer.Spec.Consumer.Auth = provider.Auth

				// populate with provider host information. we don't expose provider credentials here
				if mongodbConsumer.Spec.Provider.Hostname == "" {
					mongodbConsumer.Spec.Provider.Hostname = provider.Hostname
				}
				if mongodbConsumer.Spec.Provider.Port == "" {
					mongodbConsumer.Spec.Provider.Port = provider.Port
				}
				if mongodbConsumer.Spec.Provider.Name == "" {
					mongodbConsumer.Spec.Provider.Name = provider.Name
				}
				if mongodbConsumer.Spec.Provider.Namespace == "" {
					mongodbConsumer.Spec.Provider.Namespace = provider.Namespace
				}
				mongodbConsumer.Spec.Provider.Auth = provider.Auth
			}

			// check if service exists, get if it does, create otherwise
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mongodbConsumer.Spec.Consumer.Services.Primary,
					Labels:    labels,
					Namespace: mongodbConsumer.ObjectMeta.Namespace,
				},
				Spec: corev1.ServiceSpec{
					ExternalName: mongodbConsumer.Spec.Provider.Hostname,
					Type:         corev1.ServiceTypeExternalName,
				},
			}
			err := r.Get(context.Background(), types.NamespacedName{Namespace: req.Namespace, Name: service.ObjectMeta.Name}, service)
			if err != nil {
				opLog.Info(fmt.Sprintf("Creating service %s in namespace %s", mongodbConsumer.Spec.Consumer.Services.Primary, mongodbConsumer.ObjectMeta.Namespace))
				if err := r.Create(context.Background(), service); err != nil {
					opLog.Info(fmt.Sprintf("error creating service %s in namespace %s", mongodbConsumer.Spec.Consumer.Services.Primary, mongodbConsumer.ObjectMeta.Namespace))
					if patchErr := r.patchFailureStatus(ctx, &mongodbConsumer, fmt.Sprintf("error creating service %s: %v", mongodbConsumer.Spec.Consumer.Services.Primary, err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("unable to patch the mongodbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}
			}
			if err := r.Update(context.Background(), service); err != nil {
				opLog.Info(fmt.Sprintf("error updating service %s in namespace %s", mongodbConsumer.Spec.Consumer.Services.Primary, mongodbConsumer.ObjectMeta.Namespace))
				if patchErr := r.patchFailureStatus(ctx, &mongodbConsumer, fmt.Sprintf("error updating service %s: %v", mongodbConsumer.Spec.Consumer.Services.Primary, err), true); patchErr != nil {
					// if we can't patch the resource, just log it and return
					// next time it tries to reconcile, it will just exit here without doing anything else
					opLog.Info(fmt.Sprintf("unable to patch the mongodbconsumer with failed status, error was: %v", patchErr))
				}
				return ctrl.Result{}, nil
			}

			// The object is not being deleted, so if it does not have our finalizer,
			// then lets add the finalizer and update the object. This is equivalent
			// registering our finalizer.
			if !containsString(mongodbConsumer.ObjectMeta.Finalizers, finalizerName) {
				mongodbConsumer.ObjectMeta.Finalizers = append(mongodbConsumer.ObjectMeta.Finalizers, finalizerName)
				if err := r.Update(context.Background(), &mongodbConsumer); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			// The object is being deleted
			if containsString(mongodbConsumer.ObjectMeta.Finalizers, finalizerName) {
				// our finalizer is present, so lets handle any external dependency
				if err := r.deleteExternalResources(ctx, &mongodbConsumer, req.NamespacedName.Namespace); err != nil {
					// if fail to delete the external dependency here, return with error
					// so that it can be retried
					return ctrl.Result{}, err
				}

				// remove our finalizer from the list and update it.
				mongodbConsumer.ObjectMeta.Finalizers = removeString(mongodbConsumer.ObjectMeta.Finalizers, finalizerName)
				if err := r.Update(context.Background(), &mongodbConsumer); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager .
func (r *MongoDBConsumerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1.MongoDBConsumer{}).
		Complete(r)
}

func (r *MongoDBConsumerReconciler) deleteExternalResources(ctx context.Context, mongodbConsumer *mongodbv1.MongoDBConsumer, namespace string) error {
	opLog := r.Log.WithValues("mongodbconsumer", namespace)
	//
	// delete any external resources associated with the mongodbconsumer
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.
	// check the providers we have to see who is busy
	if _, ok := mongodbConsumer.ObjectMeta.Labels["dbaas.amazee.io/no-drop-database"]; !ok {
		provider := &MongoDBProviderInfo{}
		if err := r.getMongoDBProvider(provider, mongodbConsumer); err != nil {
			return err
		}
		if provider.Hostname == "" {
			opLog.Info("unable to determine which server to deprovision from, pausing consumer.")
			if patchErr := r.patchFailureStatus(ctx, mongodbConsumer, "Unable to determine which server to deprovision from", true); patchErr != nil {
				// if we can't patch the resource, just log it and return
				// next time it tries to reconcile, it will just exit here without doing anything else
				opLog.Info(fmt.Sprintf("unable to patch the mongodbconsumer with failed status, error was: %v", patchErr))
			}
			return errors.New("unable to determine which server to deprovision from, pausing consumer")
		}
		consumer := MongoDBConsumerInfo{
			DatabaseName: mongodbConsumer.Spec.Consumer.Database,
			Username:     mongodbConsumer.Spec.Consumer.Username,
			Password:     mongodbConsumer.Spec.Consumer.Password,
		}
		opLog.Info(fmt.Sprintf("Dropping database %s on host %s - %s/%s", consumer.DatabaseName, provider.Hostname, provider.Namespace, provider.Name))
		if err := dropDatabase(*provider, consumer); err != nil {
			return err
		}
	}
	// Delete the primary
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mongodbConsumer.Spec.Consumer.Services.Primary,
			Namespace: mongodbConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", service.ObjectMeta.Name, service.ObjectMeta.Namespace))
	if err := r.Delete(context.Background(), service); ignoreNotFound(err) != nil {
		return err
	}
	return nil
}

func truncateString(str string, num int) string {
	bnoden := str
	if len(str) > num {
		if num > 3 {
			num -= 3
		}
		bnoden = str[0:num]
	}
	return bnoden
}

var alphaNumeric = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
var dnsCompliantAlphaNumeric = []rune("abcdefghijklmnopqrstuvwxyz1234567890")
var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

func randSeq(n int, dns bool) string {
	b := make([]rune, n)
	for i := range b {
		if dns {
			b[i] = dnsCompliantAlphaNumeric[seededRand.Intn(len(dnsCompliantAlphaNumeric))]
		} else {
			b[i] = alphaNumeric[seededRand.Intn(len(alphaNumeric))]
		}
	}
	return string(b)
}

func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
func createDatabaseIfNotExist(provider MongoDBProviderInfo, consumer MongoDBConsumerInfo) error {
	// Set client options
	credential := options.Credential{
		AuthMechanism: provider.Auth.Mechanism,
		Username:      provider.Username,
		Password:      provider.Password,
		AuthSource:    provider.Auth.Source,
	}
	// support tls connections to a mongodb
	clientOptions := options.Client().
		SetAuth(credential)
	connURL := fmt.Sprintf("mongodb://%s:%s/", provider.Hostname, provider.Port)
	if provider.Auth.TLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
		}
		// connURL = fmt.Sprintf("mongodb://%s:%s/", provider.Hostname, provider.Port)
		connURL = fmt.Sprintf("mongodb://%s:%s/?sslInsecure=true", provider.Hostname, provider.Port)
		clientOptions.SetTLSConfig(tlsConfig)
	}

	// Connect to MongoDB
	client, err := mongo.Connect(context.Background(), options.MergeClientOptions(options.Client().ApplyURI(connURL), clientOptions))
	if err != nil {
		return fmt.Errorf("error creating client: %v", err)
	}

	// Check the connection
	err = client.Ping(context.Background(), readpref.Primary())
	if err != nil {
		return fmt.Errorf("error pinging server: %v", err)
	}

	// create the user
	lagoonDB := client.Database(consumer.DatabaseName)
	err = lagoonDB.RunCommand(
		context.Background(),
		bson.D{
			{Key: "createUser", Value: consumer.Username},
			{Key: "pwd", Value: consumer.Password},
			{Key: "roles", Value: bson.A{bson.D{primitive.E{
				Key: "role", Value: "readWrite"},
				{Key: "db", Value: consumer.DatabaseName}}},
			},
		}).Err()
	if err != nil {
		return fmt.Errorf("error running user creation: %v", err)
	}
	// create a lagoon collection in the database (to create the database)
	lagoonCollection := client.Database(consumer.DatabaseName).Collection("lagoon")
	_, err = lagoonCollection.InsertOne(context.Background(), bson.M{"name": "Lagoon"})
	if err != nil {
		return fmt.Errorf("error inserting database: %v", err)
	}

	// Close the connection once no longer needed
	err = client.Disconnect(context.Background())

	if err != nil {
		return fmt.Errorf("error disconnecting: %v", err)
	}
	// disconnected from mongo
	return nil
}

func dropDatabase(provider MongoDBProviderInfo, consumer MongoDBConsumerInfo) error {
	// Set client options
	credential := options.Credential{
		AuthMechanism: provider.Auth.Mechanism,
		Username:      provider.Username,
		Password:      provider.Password,
		AuthSource:    provider.Auth.Source,
	}
	// support tls connections to a mongodb
	clientOptions := options.Client().
		SetAuth(credential)
	connURL := fmt.Sprintf("mongodb://%s:%s/", provider.Hostname, provider.Port)
	if provider.Auth.TLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
		}
		// connURL = fmt.Sprintf("mongodb://%s:%s/", provider.Hostname, provider.Port)
		connURL = fmt.Sprintf("mongodb://%s:%s/?sslInsecure=true", provider.Hostname, provider.Port)
		clientOptions.SetTLSConfig(tlsConfig)
	}

	// Connect to MongoDB
	client, err := mongo.Connect(context.Background(), options.MergeClientOptions(options.Client().ApplyURI(connURL), clientOptions))
	if err != nil {
		return fmt.Errorf("error creating client: %v", err)
	}

	// Check the connection
	err = client.Ping(context.Background(), readpref.Primary())
	if err != nil {
		return fmt.Errorf("error pinging server: %v", err)
	}

	// drop the user
	lagoonDB := client.Database(consumer.DatabaseName)
	err = lagoonDB.RunCommand(
		context.Background(),
		bson.D{
			{Key: "dropUser", Value: consumer.Username},
		},
	).Err()
	if err != nil {
		return fmt.Errorf("error dropping user: %v", err)
	}
	// drop the database
	err = client.Database(consumer.DatabaseName).Drop(context.Background())
	if err != nil {
		return fmt.Errorf("error dropping database: %v", err)
	}

	// Close the connection once no longer needed
	err = client.Disconnect(context.Background())

	if err != nil {
		return fmt.Errorf("error disconnecting: %v", err)
	}
	// disconnected from mongo
	return nil
}

// get info for just one of the providers
func (r *MongoDBConsumerReconciler) getMongoDBProvider(provider *MongoDBProviderInfo, mongoDBConsumer *mongodbv1.MongoDBConsumer) error {
	providers := &mongodbv1.MongoDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.Background(), src.(*mongodbv1.MongoDBProviderList)); err != nil {
		return err
	}
	providersList := src.(*mongodbv1.MongoDBProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Hostname == mongoDBConsumer.Spec.Provider.Hostname && v.Spec.Port == mongoDBConsumer.Spec.Provider.Port {
			provider.Hostname = v.Spec.Hostname
			provider.Username = v.Spec.Username
			provider.Password = v.Spec.Password
			provider.Port = v.Spec.Port
			provider.Name = v.Name
			provider.Namespace = v.Namespace
			provider.Type = v.Spec.Type
			provider.Auth = v.Spec.Auth
		}
	}
	return nil
}

// Grab all the MongoDBProvider kinds and check each one
func (r *MongoDBConsumerReconciler) checkMongoDBProviders(provider *MongoDBProviderInfo, mongoDBConsumer *mongodbv1.MongoDBConsumer, namespaceName types.NamespacedName) error {
	opLog := r.Log.WithValues("mongodbconsumer", namespaceName)
	providers := &mongodbv1.MongoDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.Background(), src.(*mongodbv1.MongoDBProviderList)); err != nil {
		return err
	}
	providersList := src.(*mongodbv1.MongoDBProviderList)

	// We need to loop around all available providers to check their current
	// usage.
	// @TODO make this more complex and use more usage data in the calculation.
	// @TODO use the name of the provider in the log statement (not just the
	// hostname).
	var bestHostname string
	var nameSpaceName string
	lowestTableCount := -1
	for _, v := range providersList.Items {
		if noProvision, ok := v.Labels["dbaas.amazee.io/no-provision"]; ok {
			if noProvision == "true" {
				// don't provision against this provider
				continue
			}
		}
		if v.Spec.Environment == mongoDBConsumer.Spec.Environment {
			// Form a temporary connection object.
			mDBProvider := MongoDBProviderInfo{
				Hostname:  v.Spec.Hostname,
				Username:  v.Spec.Username,
				Password:  v.Spec.Password,
				Port:      v.Spec.Port,
				Name:      v.Name,
				Namespace: v.Namespace,
				Auth:      v.Spec.Auth,
			}
			currentUsage, err := getMongoDBUsage(mDBProvider, r.Log.WithValues("mongodbconsumer", namespaceName))
			if err != nil {
				return err
			}

			if lowestTableCount < 0 || currentUsage.TableCount < lowestTableCount {
				bestHostname = v.Spec.Hostname
				nameSpaceName = mDBProvider.Namespace + "/" + mDBProvider.Name
				lowestTableCount = currentUsage.TableCount
			}
		}
	}

	opLog.Info(fmt.Sprintf("Best database hostname %s has the lowest table count %v - %s", bestHostname, lowestTableCount, nameSpaceName))

	// After working out the lowest usage database, return it.
	if bestHostname != "" {
		for _, v := range providersList.Items {
			if v.Spec.Environment == mongoDBConsumer.Spec.Environment {
				if bestHostname == v.Spec.Hostname {
					provider.Hostname = v.Spec.Hostname
					provider.Username = v.Spec.Username
					provider.Password = v.Spec.Password
					provider.Port = v.Spec.Port
					provider.Name = v.Name
					provider.Namespace = v.Namespace
					provider.Type = v.Spec.Type
					provider.Auth = v.Spec.Auth
					return nil
				}
			}
		}
	}

	return errors.New("no suitable usable database servers")
}

// check the usage of the mongodb provider and return true/false if we can use it
func getMongoDBUsage(provider MongoDBProviderInfo, opLog logr.Logger) (MongoDBUsage, error) {
	currentUsage := MongoDBUsage{
		SchemaCount: 0,
		TableCount:  0,
	}

	//@TODO figure out the best way to determine an under utilised mongo

	return currentUsage, nil
}

func (r *MongoDBConsumerReconciler) patchFailureStatus(
	ctx context.Context,
	mongoDBConsumer *mongodbv1.MongoDBConsumer,
	reason string,
	failed bool,
) error {
	mergePatch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"dbaas.amazee.io/failed":        fmt.Sprintf("%v", failed),
				"dbaas.amazee.io/failed-reason": reason,
				"dbaas.amazee.io/failed-at":     time.Now().UTC().Format("2006-01-02 15:04:05"),
			},
		},
	})
	if err != nil {
		r.Log.WithValues("mongodbconsumer", types.NamespacedName{
			Name:      mongoDBConsumer.ObjectMeta.Name,
			Namespace: mongoDBConsumer.ObjectMeta.Namespace,
		}).Info(fmt.Sprintf("unable to create mergepatch for %s, error was: %v", mongoDBConsumer.ObjectMeta.Name, err))
		return nil
	}
	if err := r.Patch(ctx, mongoDBConsumer, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		r.Log.WithValues("mongodbconsumer", types.NamespacedName{
			Name:      mongoDBConsumer.ObjectMeta.Name,
			Namespace: mongoDBConsumer.ObjectMeta.Namespace,
		}).Info(fmt.Sprintf("unable to patch mongodbconsumer %s, error was: %v", mongoDBConsumer.ObjectMeta.Name, err))
		return nil
	}
	r.Log.WithValues("mongodbconsumer", types.NamespacedName{
		Name:      mongoDBConsumer.ObjectMeta.Name,
		Namespace: mongoDBConsumer.ObjectMeta.Namespace,
	}).Info(fmt.Sprintf("Patched mongodbconsumer %s", mongoDBConsumer.ObjectMeta.Name))
	return nil
}
