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
	"strings"
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
			postgresqlConsumer.Spec.Consumer.Services.Primary = truncateString(req.Name, 25) + "-" + uuid.New().String()
		}

		provider := &postgresv1.PostgreSQLProviderSpec{}
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

			if err := createDatabaseIfNotExist(*provider, postgresqlConsumer); err != nil {
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
			// some providers need to do special things, like azure
			switch provider.Type {
			case "azure":
				// the hostname can't be more than 60 characters long, we should check this and fail sooner
				hostName := strings.Split(provider.Hostname, ".")
				if len(hostName[0]) > 60 {
					opLog.Error(errors.New("Hostname is too long"), fmt.Sprintf("Hostname %s is longer than 60 characters", hostName[0]))
					return ctrl.Result{}, errors.New("Hostname is too long")
				}
				postgresqlConsumer.Spec.Consumer.Username = postgresqlConsumer.Spec.Consumer.Username + "@" + hostName[0]
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
	provider := &postgresv1.PostgreSQLProviderSpec{}
	if err := r.getPostgresSQLProvider(provider, postgresqlConsumer); err != nil {
		return err
	}
	if provider.Hostname == "" {
		return errors.New("Unable to determine server to deprovision from")
	}
	opLog.Info(fmt.Sprintf("Dropping database %s on host %s - %s/%s", postgresqlConsumer.Spec.Consumer.Database, provider.Hostname, provider.Namespace, provider.Name))
	if err := dropDbAndUser(*provider, *postgresqlConsumer); err != nil {
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

func createDatabaseIfNotExist(provider postgresv1.PostgreSQLProviderSpec, consumer postgresv1.PostgreSQLConsumer) error {
	sslMode := "disable"
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+"password=%s dbname=%s sslmode=%s", provider.Hostname, provider.Port, provider.Username, provider.Password, "postgres", sslMode)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return err
	}
	defer db.Close()
	userName := []string{consumer.Spec.Consumer.Username}
	switch provider.Type {
	case "azure":
		userName = strings.Split(consumer.Spec.Consumer.Username, "@")
	}
	// @TODO: check the equivalent of of create if not exists
	createDB := fmt.Sprintf("CREATE DATABASE \"%s\";", consumer.Spec.Consumer.Database)
	_, err = db.Exec(createDB)
	if err != nil {
		return err
	}
	// @TODO: check the equivalent of of create if not exists
	var createUser string
	createUser = fmt.Sprintf("CREATE USER \"%s\" WITH ENCRYPTED PASSWORD '%s';", userName[0], consumer.Spec.Consumer.Password)
	_, err = db.Exec(createUser)
	if err != nil {
		// if user creation fails, drop the database that gets created
		dropErr := dropDatabase(db, consumer.Spec.Consumer.Database)
		if dropErr != nil {
			return fmt.Errorf("Unable drop database after failed user creation: %v", dropErr)
		}
		return fmt.Errorf("Unable to create user %s, dropped database %s: %v", consumer.Spec.Consumer.Username, consumer.Spec.Consumer.Database, err)
	}
	var grantUser string
	grantUser = fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE \"%s\" TO \"%s\";", consumer.Spec.Consumer.Database, userName[0])
	_, err = db.Exec(grantUser)
	if err != nil {
		// if grants fails, drop the database and user that gets created
		dropErr := dropDatabase(db, consumer.Spec.Consumer.Database)
		if dropErr != nil {
			return fmt.Errorf("Unable drop database after failed user grant: %v", dropErr)
		}
		dropErr = dropUser(db, consumer, provider)
		if dropErr != nil {
			return fmt.Errorf("Unable drop user after failed user grant: %v", dropErr)
		}
		return fmt.Errorf("Unable to grant user %s permissions on database %s: %v", userName[0], consumer.Spec.Consumer.Database, err)
	}
	return nil
}

func dropDbAndUser(provider postgresv1.PostgreSQLProviderSpec, consumer postgresv1.PostgreSQLConsumer) error {
	sslMode := "disable"
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+"password=%s dbname=%s sslmode=%s", provider.Hostname, provider.Port, provider.Username, provider.Password, "postgres", sslMode)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("Unable to connect to provider: %v", err)
	}
	defer db.Close()

	err = dropDatabase(db, consumer.Spec.Consumer.Database)
	if err != nil {
		return fmt.Errorf("Unable drop database %s: %v", consumer.Spec.Consumer.Database, err)
	}
	err = dropUser(db, consumer, provider)
	if err != nil {
		return fmt.Errorf("Unable drop user %s: %v", consumer.Spec.Consumer.Username, err)
	}
	return nil
}

func dropDatabase(db *sql.DB, database string) error {
	dropDB := fmt.Sprintf("DROP DATABASE \"%s\";", database)
	_, err := db.Exec(dropDB)
	if err != nil {
		return err
	}
	return nil
}

func dropUser(db *sql.DB, consumer postgresv1.PostgreSQLConsumer, provider postgresv1.PostgreSQLProviderSpec) error {
	userName := []string{consumer.Spec.Consumer.Username}
	switch provider.Type {
	case "azure":
		userName = strings.Split(consumer.Spec.Consumer.Username, "@")
	}

	var dropUser string
	dropUser = fmt.Sprintf("DROP USER \"%s\";", userName[0])
	_, err := db.Exec(dropUser)
	if err != nil {
		return err
	}
	return nil
}

// get info for just one of the providers
func (r *PostgreSQLConsumerReconciler) getPostgresSQLProvider(provider *postgresv1.PostgreSQLProviderSpec, postgresSQLConsumer *postgresv1.PostgreSQLConsumer) error {
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
			provider.Name = v.ObjectMeta.Name
			provider.Namespace = v.ObjectMeta.Namespace
			provider.Type = v.Spec.Type
		}
	}
	return nil
}

// Grab all the PostgresSQLProvider kinds and check each one
func (r *PostgreSQLConsumerReconciler) checkPostgresSQLProviders(provider *postgresv1.PostgreSQLProviderSpec, postgresSQLConsumer *postgresv1.PostgreSQLConsumer, namespaceName types.NamespacedName) error {
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
			mDBProvider := postgresv1.PostgreSQLProviderSpec{
				Hostname:  v.Spec.Hostname,
				Username:  v.Spec.Username,
				Password:  v.Spec.Password,
				Port:      v.Spec.Port,
				Name:      v.ObjectMeta.Name,
				Namespace: v.ObjectMeta.Namespace,
				Type:      v.Spec.Type,
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
					provider.Name = v.ObjectMeta.Name
					provider.Namespace = v.ObjectMeta.Namespace
					provider.Type = v.Spec.Type
					return nil
				}
			}
		}
	}

	return errors.New("No suitable usable database servers")
}

// check the usage of the postgresql provider and return true/false if we can use it
func getPostgresSQLUsage(provider postgresv1.PostgreSQLProviderSpec, opLog logr.Logger) (PostgreSQLUsage, error) {
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
