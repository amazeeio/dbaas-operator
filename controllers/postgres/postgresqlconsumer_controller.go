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
	"database/sql"
	"errors"
	"fmt"
	"math/rand"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	postgresv1 "github.com/amazeeio/dbaas-operator/apis/postgres/v1"

	// postgres driver
	_ "github.com/lib/pq"
)

// PostgreSQLConsumerReconciler reconciles a PostgreSQLConsumer object
type PostgreSQLConsumerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// PostgreSQLProviderInfo .
type PostgreSQLProviderInfo struct {
	Hostname             string
	ReadReplicaHostnames []string
	Username             string
	Password             string
	Port                 string
	Name                 string
	Namespace            string
}

// PostgreSQLConsumerInfo .
type PostgreSQLConsumerInfo struct {
	DatabaseName string
	Username     string
	Password     string
}

// PostgreSQLUsage .
type PostgreSQLUsage struct {
	SchemaCount int
	TableCount  int
}

const (
	// LabelAppName for discovery.
	LabelAppName = "postgres.amazee.io/service-name"
	// LabelAppType for discovery.
	LabelAppType = "postgres.amazee.io/type"
	// LabelAppManaged for discovery.
	LabelAppManaged = "postgres.amazee.io/managed-by"
)

// +kubebuilder:rbac:groups=postgres.amazee.io,resources=postgresqlconsumers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgres.amazee.io,resources=postgresqlproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=postgres.amazee.io,resources=postgresqlconsumers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=list;get;watch;create;update;patch;delete

func (r *PostgreSQLConsumerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("postgresqlconsumer", req.NamespacedName)

	var postgresqlConsumer postgresv1.PostgreSQLConsumer
	if err := r.Get(ctx, req.NamespacedName, &postgresqlConsumer); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.consumer.postgres.amazee.io/v1"

	labels := map[string]string{
		LabelAppName:    postgresqlConsumer.ObjectMeta.Name,
		LabelAppType:    "postgresqlconsumer",
		LabelAppManaged: "dbaas-operator",
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if postgresqlConsumer.ObjectMeta.DeletionTimestamp.IsZero() {
		// set up the new credentials
		if postgresqlConsumer.Spec.Consumer.Database == "" {
			postgresqlConsumer.Spec.Consumer.Database = truncateString(req.NamespacedName.Namespace, 50) + "_" + randSeq(5, false)
		}
		if postgresqlConsumer.Spec.Consumer.Username == "" {
			postgresqlConsumer.Spec.Consumer.Username = truncateString(req.NamespacedName.Namespace, 10) + "_" + randSeq(5, false)
		}
		if postgresqlConsumer.Spec.Consumer.Password == "" {
			postgresqlConsumer.Spec.Consumer.Password = randSeq(24, false)
		}
		if postgresqlConsumer.Spec.Consumer.Services.Primary == "" {
			postgresqlConsumer.Spec.Consumer.Services.Primary = truncateString(req.Name, 27) + "-" + uuid.New().String()
		}

		provider := &PostgreSQLProviderInfo{}
		// if we haven't got any provider specific information pre-defined, we should query the providers to get one
		if postgresqlConsumer.Spec.Provider.Hostname == "" || postgresqlConsumer.Spec.Provider.Port == "" {
			opLog.Info(fmt.Sprintf("Attempting to create database %s on any usable postgresql provider", postgresqlConsumer.Spec.Consumer.Database))
			// check the providers we have to see who is busy
			if err := r.checkPostgresSQLProviders(provider, &postgresqlConsumer, req.NamespacedName); err != nil {
				return ctrl.Result{}, err
			}
			if provider.Hostname == "" {
				opLog.Info("No suitable postgresql providers found, bailing")
				return ctrl.Result{}, nil
			}

			consumer := PostgreSQLConsumerInfo{
				DatabaseName: postgresqlConsumer.Spec.Consumer.Database,
				Username:     postgresqlConsumer.Spec.Consumer.Username,
				Password:     postgresqlConsumer.Spec.Consumer.Password,
			}

			if err := createDatabaseIfNotExist(*provider, consumer); err != nil {
				return ctrl.Result{}, err
			}

			// populate with provider host information. we don't expose provider credentials here
			if postgresqlConsumer.Spec.Provider.Hostname == "" {
				postgresqlConsumer.Spec.Provider.Hostname = provider.Hostname
			}
			if postgresqlConsumer.Spec.Provider.Port == "" {
				postgresqlConsumer.Spec.Provider.Port = provider.Port
			}
			if postgresqlConsumer.Spec.Provider.Name == "" {
				postgresqlConsumer.Spec.Provider.Name = provider.Name
			}
			if postgresqlConsumer.Spec.Provider.Namespace == "" {
				postgresqlConsumer.Spec.Provider.Namespace = provider.Namespace
			}
		}

		// check if service exists, get if it does, create otherwise
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      postgresqlConsumer.Spec.Consumer.Services.Primary,
				Labels:    labels,
				Namespace: postgresqlConsumer.ObjectMeta.Namespace,
			},
			Spec: corev1.ServiceSpec{
				ExternalName: postgresqlConsumer.Spec.Provider.Hostname,
				Type:         corev1.ServiceTypeExternalName,
			},
		}
		err := r.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: service.ObjectMeta.Name}, service)
		if err != nil {
			opLog.Info(fmt.Sprintf("Creating service %s in namespace %s", postgresqlConsumer.Spec.Consumer.Services.Primary, postgresqlConsumer.ObjectMeta.Namespace))
			if err := r.Create(context.Background(), service); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Update(context.Background(), service); err != nil {
			return ctrl.Result{}, err
		}

		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(postgresqlConsumer.ObjectMeta.Finalizers, finalizerName) {
			postgresqlConsumer.ObjectMeta.Finalizers = append(postgresqlConsumer.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &postgresqlConsumer); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(postgresqlConsumer.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&postgresqlConsumer, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			postgresqlConsumer.ObjectMeta.Finalizers = removeString(postgresqlConsumer.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &postgresqlConsumer); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *PostgreSQLConsumerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1.PostgreSQLConsumer{}).
		Complete(r)
}

func (r *PostgreSQLConsumerReconciler) deleteExternalResources(postgresqlConsumer *postgresv1.PostgreSQLConsumer, namespace string) error {
	opLog := r.Log.WithValues("postgresqlconsumer", namespace)
	//
	// delete any external resources associated with the postgresqlconsumer
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.
	// check the providers we have to see who is busy
	provider := &PostgreSQLProviderInfo{}
	if err := r.getPostgresSQLProvider(provider, postgresqlConsumer); err != nil {
		return err
	}
	if provider.Hostname == "" {
		return errors.New("Unable to determine server to deprovision from")
	}
	consumer := PostgreSQLConsumerInfo{
		DatabaseName: postgresqlConsumer.Spec.Consumer.Database,
		Username:     postgresqlConsumer.Spec.Consumer.Username,
		Password:     postgresqlConsumer.Spec.Consumer.Password,
	}
	opLog.Info(fmt.Sprintf("Dropping database %s on host %s - %s/%s", consumer.DatabaseName, provider.Hostname, provider.Namespace, provider.Name))
	if err := dropDatabase(*provider, consumer); err != nil {
		return err
	}
	// Delete the primary
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      postgresqlConsumer.Spec.Consumer.Services.Primary,
			Namespace: postgresqlConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", service.ObjectMeta.Name, service.ObjectMeta.Namespace))
	if err := r.Delete(context.TODO(), service); ignoreNotFound(err) != nil {
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

func randSeq(n int, dns bool) string {
	b := make([]rune, n)
	for i := range b {
		if dns {
			b[i] = dnsCompliantAlphaNumeric[rand.Intn(len(dnsCompliantAlphaNumeric))]
		} else {
			b[i] = alphaNumeric[rand.Intn(len(alphaNumeric))]
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

func createDatabaseIfNotExist(provider PostgreSQLProviderInfo, consumer PostgreSQLConsumerInfo) error {
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+"password=%s dbname=%s sslmode=disable", provider.Hostname, provider.Port, provider.Username, provider.Password, "postgres")
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return err
	}
	defer db.Close()

	// @TODO: check the equivalent of of create if not exists
	_, err = db.Exec("CREATE DATABASE " + consumer.DatabaseName + ";")
	// _, err = db.Exec("SELECT 'CREATE DATABASE `" + consumer.DatabaseName + "`'	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '`" + consumer.DatabaseName + "`')\gexec")
	if err != nil {
		return err
	}
	// @TODO: check the equivalent of of create if not exists
	_, err = db.Exec("CREATE USER " + consumer.Username + " WITH ENCRYPTED PASSWORD '" + consumer.Password + "';")
	// _, err = db.Exec("IF NOT EXISTS (SELECT * FROM pg_user WHERE username = `" + consumer.Username + "`) BEGIN CREATE ROLE my_user LOGIN PASSWORD '" + consumer.Password + "'; END")
	if err != nil {
		return err
	}
	_, err = db.Exec("GRANT ALL PRIVILEGES ON DATABASE " + consumer.DatabaseName + " TO " + consumer.Username + ";")
	if err != nil {
		return err
	}
	return nil
}

func dropDatabase(provider PostgreSQLProviderInfo, consumer PostgreSQLConsumerInfo) error {
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+"password=%s dbname=%s sslmode=disable", provider.Hostname, provider.Port, provider.Username, provider.Password, "postgres")
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("DROP DATABASE " + consumer.DatabaseName + ";")
	if err != nil {
		return err
	}
	// delete db user
	_, err = db.Exec("DROP USER " + consumer.Username + ";")
	if err != nil {
		return err
	}
	return nil
}

// get info for just one of the providers
func (r *PostgreSQLConsumerReconciler) getPostgresSQLProvider(provider *PostgreSQLProviderInfo, postgresSQLConsumer *postgresv1.PostgreSQLConsumer) error {
	providers := &postgresv1.PostgreSQLProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*postgresv1.PostgreSQLProviderList)); err != nil {
		return err
	}
	providersList := src.(*postgresv1.PostgreSQLProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Hostname == postgresSQLConsumer.Spec.Provider.Hostname && v.Spec.Port == postgresSQLConsumer.Spec.Provider.Port {
			provider.Hostname = v.Spec.Hostname
			provider.Username = v.Spec.Username
			provider.Password = v.Spec.Password
			provider.Port = v.Spec.Port
			provider.Name = v.Name
			provider.Namespace = v.Namespace
		}
	}
	return nil
}

// Grab all the PostgresSQLProvider kinds and check each one
func (r *PostgreSQLConsumerReconciler) checkPostgresSQLProviders(provider *PostgreSQLProviderInfo, postgresSQLConsumer *postgresv1.PostgreSQLConsumer, namespaceName types.NamespacedName) error {
	opLog := r.Log.WithValues("postgresqlconsumer", namespaceName)
	providers := &postgresv1.PostgreSQLProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*postgresv1.PostgreSQLProviderList)); err != nil {
		return err
	}
	providersList := src.(*postgresv1.PostgreSQLProviderList)

	// We need to loop around all available providers to check their current
	// usage.
	// @TODO make this more complex and use more usage data in the calculation.
	// @TODO use the name of the provider in the log statement (not just the
	// hostname).
	var bestHostname string
	var nameSpaceName string
	lowestTableCount := -1
	for _, v := range providersList.Items {
		if v.Spec.Environment == postgresSQLConsumer.Spec.Environment {
			// Form a temporary connection object.
			mDBProvider := PostgreSQLProviderInfo{
				Hostname:  v.Spec.Hostname,
				Username:  v.Spec.Username,
				Password:  v.Spec.Password,
				Port:      v.Spec.Port,
				Name:      v.Name,
				Namespace: v.Namespace,
			}
			currentUsage, err := getPostgresSQLUsage(mDBProvider, r.Log.WithValues("postgresqlconsumer", namespaceName))
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
			if v.Spec.Environment == postgresSQLConsumer.Spec.Environment {
				if bestHostname == v.Spec.Hostname {
					provider.Hostname = v.Spec.Hostname
					provider.Username = v.Spec.Username
					provider.Password = v.Spec.Password
					provider.Port = v.Spec.Port
					provider.Name = v.Name
					provider.Namespace = v.Namespace
					return nil
				}
			}
		}
	}

	return errors.New("No suitable usable database servers")
}

// check the usage of the postgresql provider and return true/false if we can use it
func getPostgresSQLUsage(provider PostgreSQLProviderInfo, opLog logr.Logger) (PostgreSQLUsage, error) {
	currentUsage := PostgreSQLUsage{
		SchemaCount: 0,
		TableCount:  0,
	}
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+"password=%s dbname=%s sslmode=disable", provider.Hostname, provider.Port, provider.Username, provider.Password, "postgres")
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return currentUsage, err
	}
	defer db.Close()

	result, err := db.Query(`SELECT COUNT(1) AS TableCount
                            FROM information_schema.tables
                            WHERE table_schema NOT IN ('information_schema','postgres', 'sys');`)
	if err != nil {
		return currentUsage, err
	}
	for result.Next() {
		var value int
		err = result.Scan(&value)
		if err != nil {
			return currentUsage, err
		}
		opLog.Info(fmt.Sprintf("Table count on host %s has reached %v - %s/%s", provider.Hostname, value, provider.Namespace, provider.Name))
		currentUsage.TableCount = value
	}

	result, err = db.Query(`SELECT COUNT(*) AS SchemaCount
	                        FROM information_schema.SCHEMATA
	                        WHERE schema_name NOT IN ('information_schema','postgres', 'sys');`)
	if err != nil {
		return currentUsage, err
	}
	for result.Next() {
		var value int
		err = result.Scan(&value)
		if err != nil {
			return currentUsage, err
		}
		opLog.Info(fmt.Sprintf("Schema count on host %s has reached %v - %s/%s", provider.Hostname, value, provider.Namespace, provider.Name))
		currentUsage.SchemaCount = value
	}

	return currentUsage, nil
}
