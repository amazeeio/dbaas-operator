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
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mariadbv1 "github.com/amazeeio/dbaas-operator/apis/mariadb/v1"

	"database/sql"

	// mysql driver
	_ "github.com/go-sql-driver/mysql"
)

// MariaDBConsumerReconciler reconciles a MariaDBConsumer object
type MariaDBConsumerReconciler struct {
	client.Client
	Log                        logr.Logger
	Scheme                     *runtime.Scheme
	Environment                string
	Hostname                   string
	ServiceHostname            string
	ReadReplicaHostname        string
	ServiceReadReplicaHostname string
	Password                   string
	Port                       string
	Username                   string
	Provider                   struct {
		Name      string
		Namespace string
	}
	Consumer struct {
		Database string
		Password string
		Username string
		Services struct {
			ServiceHostname            string
			ServiceReadReplicaHostname string
		}
	}
}

// MariaDBUsage .
type MariaDBUsage struct {
	SchemaCount int
	TableCount  int
}

const (
	// LabelAppName for discovery.
	LabelAppName = "mariadb.amazee.io/service-name"
	// LabelAppType for discovery.
	LabelAppType = "mariadb.amazee.io/type"
	// LabelAppManaged for discovery.
	LabelAppManaged = "mariadb.amazee.io/managed-by"
)

// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbconsumers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbconsumers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=list;get;watch;create;update;patch;delete

// Reconcile .
func (r *MariaDBConsumerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// ctx := context.Background()
	opLog := r.Log.WithValues("mariadbconsumer", req.NamespacedName)

	var mariaDBConsumer mariadbv1.MariaDBConsumer
	if err := r.Get(ctx, req.NamespacedName, &mariaDBConsumer); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.consumer.mariadb.amazee.io/v1"

	labels := map[string]string{
		LabelAppName:    mariaDBConsumer.ObjectMeta.Name,
		LabelAppType:    "mariadbconsumer",
		LabelAppManaged: "dbaas-operator",
	}

	// check if the consumer is in a failed state, this prevents it from constantly re-trying to provision if its failed.
	// it also don't be able to be removed if it is in a failed state, as there could be an actual reason
	// and some human intervention required to prevent possible data loss
	skip := "false"
	if val, ok := mariaDBConsumer.ObjectMeta.Annotations["dbaas.amazee.io/failed"]; !ok {
		skip = val
	}
	if skip != "true" {
		// examine DeletionTimestamp to determine if object is under deletion
		if mariaDBConsumer.ObjectMeta.DeletionTimestamp.IsZero() {
			// set up the new credentials
			if mariaDBConsumer.Spec.Consumer.Database == "" {
				mariaDBConsumer.Spec.Consumer.Database = truncateString(req.NamespacedName.Namespace, 50) + "_" + randSeq(5, false)
			}
			if mariaDBConsumer.Spec.Consumer.Username == "" {
				mariaDBConsumer.Spec.Consumer.Username = truncateString(req.NamespacedName.Namespace, 10) + "_" + randSeq(5, false)
			}
			if mariaDBConsumer.Spec.Consumer.Password == "" {
				mariaDBConsumer.Spec.Consumer.Password = randSeq(24, false)
			}
			if mariaDBConsumer.Spec.Consumer.Services.Primary == "" {
				mariaDBConsumer.Spec.Consumer.Services.Primary = truncateString(req.Name, 25) + "-" + uuid.New().String()
			}

			provider := &mariadbv1.MariaDBProviderSpec{}
			// if we haven't got any provider specific information pre-defined, we should query the providers to get one
			if mariaDBConsumer.Spec.Provider.Hostname == "" || mariaDBConsumer.Spec.Provider.Port == "" || len(mariaDBConsumer.Spec.Provider.ReadReplicaHostnames) == 0 {
				opLog.Info(fmt.Sprintf("Attempting to create database %s on any usable mariadb provider", mariaDBConsumer.Spec.Consumer.Database))
				// check the providers we have to see who is busy
				if err := r.checkMariaDBProviders(provider, &mariaDBConsumer, req.NamespacedName); err != nil {
					opLog.Info(fmt.Sprintf("Error checking the providers in the cluster."))
					if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Error checking the providers in the cluster: %v", err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}
				if provider.Hostname == "" {
					opLog.Info("No suitable mariadb providers found, bailing")
					return ctrl.Result{}, nil
				}

				// populate with provider host information. we don't expose provider credentials here
				if mariaDBConsumer.Spec.Provider.Hostname == "" {
					mariaDBConsumer.Spec.Provider.Hostname = provider.Hostname
				}
				if mariaDBConsumer.Spec.Provider.Port == "" {
					mariaDBConsumer.Spec.Provider.Port = provider.Port
				}
				// some providers need to do special things, like azure
				switch provider.Type {
				case "azure":
					// the hostname can't be more than 60 characters long, we should check this and fail sooner
					hostName := strings.Split(provider.Hostname, ".")
					if len(hostName[0]) > 60 {
						opLog.Info(fmt.Sprintf("Hostname %s is longer than 60 characters", hostName[0]))
						if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Hostname %s is longer than 60 characters: %v", hostName[0], errors.New("Hostname is too long")), true); patchErr != nil {
							// if we can't patch the resource, just log it and return
							// next time it tries to reconcile, it will just exit here without doing anything else
							opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
						}
						return ctrl.Result{}, nil
					}
					mariaDBConsumer.Spec.Consumer.Username = mariaDBConsumer.Spec.Consumer.Username + "@" + hostName[0]
				}
				if len(mariaDBConsumer.Spec.Provider.ReadReplicaHostnames) == 0 {
					for _, replica := range provider.ReadReplicaHostnames {
						mariaDBConsumer.Spec.Provider.ReadReplicaHostnames = append(mariaDBConsumer.Spec.Provider.ReadReplicaHostnames, replica)
					}
				}
				if mariaDBConsumer.Spec.Provider.Name == "" {
					mariaDBConsumer.Spec.Provider.Name = provider.Name
				}
				if mariaDBConsumer.Spec.Provider.Namespace == "" {
					mariaDBConsumer.Spec.Provider.Namespace = provider.Namespace
				}

				// once we have all the consumer and provider info, attempt to create the consumer user and database
				opLog.Info(fmt.Sprintf("Proceeding to create database %s on %s/%s for user %s", mariaDBConsumer.Spec.Consumer.Database, provider.Namespace, provider.Name, mariaDBConsumer.Spec.Consumer.Username))
				if err := createDatabaseIfNotExist(*provider, mariaDBConsumer); err != nil {
					opLog.Info("Unable to create database")
					if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Unable to create database %v", err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}

			}

			// check if service exists, get if it does, create otherwise
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mariaDBConsumer.Spec.Consumer.Services.Primary,
					Labels:    labels,
					Namespace: mariaDBConsumer.ObjectMeta.Namespace,
				},
				Spec: corev1.ServiceSpec{
					ExternalName: mariaDBConsumer.Spec.Provider.Hostname,
					Type:         corev1.ServiceTypeExternalName,
				},
			}
			err := r.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: service.ObjectMeta.Name}, service)
			if err != nil {
				opLog.Info(fmt.Sprintf("Creating service %s in namespace %s", mariaDBConsumer.Spec.Consumer.Services.Primary, mariaDBConsumer.ObjectMeta.Namespace))
				if err := r.Create(context.Background(), service); err != nil {
					opLog.Info(fmt.Sprintf("Error creating service %s in namespace %s", mariaDBConsumer.Spec.Consumer.Services.Primary, mariaDBConsumer.ObjectMeta.Namespace))
					if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Error creating service %s: %v", mariaDBConsumer.Spec.Consumer.Services.Primary, err), true); patchErr != nil {
						// if we can't patch the resource, just log it and return
						// next time it tries to reconcile, it will just exit here without doing anything else
						opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
					}
					return ctrl.Result{}, nil
				}
			}
			if err := r.Update(context.Background(), service); err != nil {
				opLog.Info(fmt.Sprintf("Error updating service %s in namespace %s", mariaDBConsumer.Spec.Consumer.Services.Primary, mariaDBConsumer.ObjectMeta.Namespace))
				if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Error updating service %s: %v", mariaDBConsumer.Spec.Consumer.Services.Primary, err), true); patchErr != nil {
					// if we can't patch the resource, just log it and return
					// next time it tries to reconcile, it will just exit here without doing anything else
					opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
				}
				return ctrl.Result{}, nil
			}
			// check if read replica service exists, get if it does, create otherwise
			if len(mariaDBConsumer.Spec.Consumer.Services.Replicas) == 0 {
				for _, replica := range mariaDBConsumer.Spec.Provider.ReadReplicaHostnames {
					replicaName := truncateString("readreplica-"+req.Name, 25) + "-" + uuid.New().String()
					mariaDBConsumer.Spec.Consumer.Services.Replicas = append(mariaDBConsumer.Spec.Consumer.Services.Replicas, replicaName)
					serviceRR := &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      replicaName,
							Labels:    labels,
							Namespace: mariaDBConsumer.ObjectMeta.Namespace,
						},
						Spec: corev1.ServiceSpec{
							ExternalName: replica,
							Type:         corev1.ServiceTypeExternalName,
						},
					}
					err = r.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: serviceRR.ObjectMeta.Name}, serviceRR)
					if err != nil {
						opLog.Info(fmt.Sprintf("Creating service %s in namespace %s", replicaName, mariaDBConsumer.ObjectMeta.Namespace))
						if err := r.Create(context.Background(), serviceRR); err != nil {
							opLog.Info(fmt.Sprintf("Error creating service %s in namespace %s", replicaName, mariaDBConsumer.ObjectMeta.Namespace))
							if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Error creating service %s: %v", replicaName, err), true); patchErr != nil {
								// if we can't patch the resource, just log it and return
								// next time it tries to reconcile, it will just exit here without doing anything else
								opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
							}
							return ctrl.Result{}, nil
						}
					}
					if err := r.Update(context.Background(), serviceRR); err != nil {
						opLog.Info(fmt.Sprintf("Error updating service %s in namespace %s", replicaName, mariaDBConsumer.ObjectMeta.Namespace))
						if patchErr := r.patchFailureStatus(ctx, &mariaDBConsumer, fmt.Sprintf("Error updating service %s: %v", replicaName, err), true); patchErr != nil {
							// if we can't patch the resource, just log it and return
							// next time it tries to reconcile, it will just exit here without doing anything else
							opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
						}
						return ctrl.Result{}, nil
					}
				}
			}

			// The object is not being deleted, so if it does not have our finalizer,
			// then lets add the finalizer and update the object. This is equivalent
			// registering our finalizer.
			if !containsString(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName) {
				mariaDBConsumer.ObjectMeta.Finalizers = append(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName)
				if err := r.Update(context.Background(), &mariaDBConsumer); err != nil {
					opLog.Error(err, fmt.Sprintf("Error updating consumer %s in namespace %s", mariaDBConsumer.ObjectMeta.Name, mariaDBConsumer.ObjectMeta.Namespace))
					return ctrl.Result{}, err
				}
			}
		} else {
			// The object is being deleted
			if containsString(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName) {
				// our finalizer is present, so lets handle any external dependency
				if err := r.deleteExternalResources(ctx, &mariaDBConsumer, req.NamespacedName.Namespace); err != nil {
					// if fail to delete the external dependency here, return with error
					// so that it can be retried
					return ctrl.Result{}, err
				}

				// remove our finalizer from the list and update it.
				mariaDBConsumer.ObjectMeta.Finalizers = removeString(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName)
				if err := r.Update(context.Background(), &mariaDBConsumer); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager .
func (r *MariaDBConsumerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.MariaDBConsumer{}).
		Complete(r)
}

func (r *MariaDBConsumerReconciler) deleteExternalResources(ctx context.Context, mariaDBConsumer *mariadbv1.MariaDBConsumer, namespace string) error {
	opLog := r.Log.WithValues("mariadbconsumer", namespace)
	//
	// delete any external resources associated with the mariadbconsumer
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.
	// check the providers we have
	provider := &mariadbv1.MariaDBProviderSpec{}
	if err := r.getMariaDBProvider(provider, mariaDBConsumer, opLog); err != nil {
		return err
	}
	if provider.Hostname == "" {
		// if we can't determine the server to deprovision from
		opLog.Info(fmt.Sprintf("Unable to determine which server to deprovision from, pausing consumer."))
		if patchErr := r.patchFailureStatus(ctx, mariaDBConsumer, "Unable to determine which server to deprovision from", true); patchErr != nil {
			// if we can't patch the resource, just log it and return
			// next time it tries to reconcile, it will just exit here without doing anything else
			opLog.Info(fmt.Sprintf("Unable to patch the mariadbconsumer with failed status, error was: %v", patchErr))
		}
		return errors.New("unable to determine which server to deprovision from, pausing consumer")
	}
	opLog.Info(fmt.Sprintf("Dropping database %s on host %s - %s/%s", mariaDBConsumer.Spec.Consumer.Database, provider.Hostname, provider.Namespace, provider.Name))
	if err := dropDbAndUser(*provider, *mariaDBConsumer); err != nil {
		// If the database doesn't exist, then we can still continue deprovisioning.
		if strings.Contains(err.Error(), "database doesn't exist") {
			opLog.Info(fmt.Sprintf("Error dropping database: %v, continuing with deprovisioning.", err))
		} else {
			return err
		}
	}
	// Delete the primary
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariaDBConsumer.Spec.Consumer.Services.Primary,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", service.ObjectMeta.Name, service.ObjectMeta.Namespace))
	if err := r.Delete(context.TODO(), service); ignoreNotFound(err) != nil {
		return fmt.Errorf("Unable to delete service %s in %s: %v", service.ObjectMeta.Name, service.ObjectMeta.Namespace, err)
	}
	// Delete the read replicas
	for _, replica := range mariaDBConsumer.Spec.Consumer.Services.Replicas {
		serviceRR := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      replica,
				Namespace: mariaDBConsumer.ObjectMeta.Namespace,
			},
		}
		opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", serviceRR.ObjectMeta.Name, serviceRR.ObjectMeta.Namespace))
		if err := r.Delete(context.TODO(), serviceRR); ignoreNotFound(err) != nil {
			return fmt.Errorf("Unable to delete service %s in %s: %v", serviceRR.ObjectMeta.Name, serviceRR.ObjectMeta.Namespace, err)
		}
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

func createDatabaseIfNotExist(provider mariadbv1.MariaDBProviderSpec, consumer mariadbv1.MariaDBConsumer) error {
	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
	if err != nil {
		return fmt.Errorf("Unable to connect to provider: %v", err)
	}
	defer db.Close()

	createDB := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", consumer.Spec.Consumer.Database)
	_, err = db.Exec(createDB)
	if err != nil {
		return fmt.Errorf("Unable create database: %v", err)
	}
	var createUser string
	switch provider.Type {
	case "azure":
		userName := strings.Split(consumer.Spec.Consumer.Username, "@")
		// hostName := strings.Split(provider.Hostname, ".")
		createUser = fmt.Sprintf("CREATE USER IF NOT EXISTS `%s`@'%%' IDENTIFIED BY '%s';", userName[0], consumer.Spec.Consumer.Password)
	default:
		createUser = fmt.Sprintf("CREATE USER IF NOT EXISTS `%s`@'%%' IDENTIFIED BY '%s';", consumer.Spec.Consumer.Username, consumer.Spec.Consumer.Password)
	}
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
	switch provider.Type {
	case "azure":
		userName := strings.Split(consumer.Spec.Consumer.Username, "@")
		// hostName := strings.Split(provider.Hostname, ".")
		grantUser = fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, REFERENCES, INDEX, ALTER, CREATE TEMPORARY TABLES, LOCK TABLES, EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER ON `%s`.* TO `%s`@'%%';", consumer.Spec.Consumer.Database, userName[0])
	default:
		grantUser = fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, REFERENCES, INDEX, ALTER, CREATE TEMPORARY TABLES, LOCK TABLES, EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER ON `%s`.* TO `%s`@'%%';", consumer.Spec.Consumer.Database, consumer.Spec.Consumer.Username)
	}
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
		return fmt.Errorf("Unable to grant user %s permissions on database %s: %v", consumer.Spec.Consumer.Username, consumer.Spec.Consumer.Database, err)
	}
	_, err = db.Exec("FLUSH PRIVILEGES;")
	if err != nil {
		return fmt.Errorf("Unable flush privileges: %v", err)
	}
	return nil
}

func dropDbAndUser(provider mariadbv1.MariaDBProviderSpec, consumer mariadbv1.MariaDBConsumer) error {
	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
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
	_, err = db.Exec("FLUSH PRIVILEGES;")
	if err != nil {
		return fmt.Errorf("Unable flush privileges: %v", err)
	}
	return nil
}

func dropDatabase(db *sql.DB, database string) error {
	dropDB := fmt.Sprintf("DROP DATABASE `%s`;", database)
	_, err := db.Exec(dropDB)
	if err != nil {
		return err
	}
	return nil
}

func dropUser(db *sql.DB, consumer mariadbv1.MariaDBConsumer, provider mariadbv1.MariaDBProviderSpec) error {
	var dropUser string
	switch provider.Type {
	case "azure":
		userName := strings.Split(consumer.Spec.Consumer.Username, "@")
		// hostName := strings.Split(provider.Hostname, ".")
		dropUser = fmt.Sprintf("DROP USER `%s`@'%%';", userName[0])
	default:
		dropUser = fmt.Sprintf("DROP USER `%s`@'%%';", consumer.Spec.Consumer.Username)
	}
	_, err := db.Exec(dropUser)
	if err != nil {
		return err
	}
	return nil
}

// check the usage of the mariadb provider and return true/false if we can use it
func getMariaDBUsage(provider mariadbv1.MariaDBProviderSpec, opLog logr.Logger) (MariaDBUsage, error) {
	currentUsage := MariaDBUsage{
		SchemaCount: 0,
		TableCount:  0,
	}

	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
	if err != nil {
		return currentUsage, fmt.Errorf("Unable to connect to %s using %s: %v", provider.Hostname, provider.Username, err)
	}
	defer db.Close()
	var tableCountQuery = `SELECT COUNT(1) AS TableCount
	FROM information_schema.tables
	WHERE table_schema NOT IN ('information_schema','mysql', 'sys');`
	result, err := db.Query(tableCountQuery)
	if err != nil {
		return currentUsage, fmt.Errorf("Unable to execute query %s on %s: %v", tableCountQuery, provider.Hostname, err)
	}
	for result.Next() {
		var value int
		err = result.Scan(&value)
		if err != nil {
			return currentUsage, fmt.Errorf("Unable to scan results for query %s on %s: %v", tableCountQuery, provider.Hostname, err)
		}
		opLog.Info(fmt.Sprintf("Table count on host %s has reached %v - %s/%s", provider.Hostname, value, provider.Namespace, provider.Name))
		currentUsage.TableCount = value
	}

	var schemaCountQuery = `SELECT COUNT(*) AS SchemaCount
	FROM information_schema.SCHEMATA
	WHERE schema_name NOT IN ('information_schema','mysql', 'sys');`
	result, err = db.Query(schemaCountQuery)
	if err != nil {
		return currentUsage, fmt.Errorf("Unable to execute query %s on %s: %v", schemaCountQuery, provider.Hostname, err)
	}
	for result.Next() {
		var value int
		err = result.Scan(&value)
		if err != nil {
			return currentUsage, fmt.Errorf("Unable to scan results for query %s on %s: %v", schemaCountQuery, provider.Hostname, err)
		}
		opLog.Info(fmt.Sprintf("Schema count on host %s has reached %v - %s/%s", provider.Hostname, value, provider.Namespace, provider.Name))
		currentUsage.SchemaCount = value
	}

	return currentUsage, nil
}

// Grab all the MariaDBProvider kinds and check each one
func (r *MariaDBConsumerReconciler) checkMariaDBProviders(provider *mariadbv1.MariaDBProviderSpec, mariaDBConsumer *mariadbv1.MariaDBConsumer, namespaceName types.NamespacedName) error {
	opLog := r.Log.WithValues("mariadbconsumer", namespaceName)
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return fmt.Errorf("Unable to list providers in the cluster, there may be none or something went wrong: %v", err)
	}
	providersList := src.(*mariadbv1.MariaDBProviderList)

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
		if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
			// Form a temporary connection object.
			mDBProvider := mariadbv1.MariaDBProviderSpec{
				Hostname:  v.Spec.Hostname,
				Username:  v.Spec.Username,
				Password:  v.Spec.Password,
				Port:      v.Spec.Port,
				Name:      v.ObjectMeta.Name,
				Namespace: v.ObjectMeta.Namespace,
				Type:      v.Spec.Type,
			}
			currentUsage, err := getMariaDBUsage(mDBProvider, r.Log.WithValues("mariadbconsumer", namespaceName))
			if err != nil {
				return fmt.Errorf("Unable to get provider usage, something went wrong: %v", err)
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
			if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
				if bestHostname == v.Spec.Hostname {
					provider.Hostname = v.Spec.Hostname
					provider.ReadReplicaHostnames = v.Spec.ReadReplicaHostnames
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

// get info for just one of the providers
func (r *MariaDBConsumerReconciler) getMariaDBProvider(provider *mariadbv1.MariaDBProviderSpec, mariaDBConsumer *mariadbv1.MariaDBConsumer, opLog logr.Logger) error {
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return fmt.Errorf("Unable to list providers in the cluster, there may be none or something went wrong: %v", err)
	}
	providersList := src.(*mariadbv1.MariaDBProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
			if v.Spec.Hostname == mariaDBConsumer.Spec.Provider.Hostname && v.Spec.Port == mariaDBConsumer.Spec.Provider.Port {
				provider.Hostname = v.Spec.Hostname
				provider.ReadReplicaHostnames = v.Spec.ReadReplicaHostnames
				provider.Username = v.Spec.Username
				provider.Password = v.Spec.Password
				provider.Port = v.Spec.Port
				provider.Name = v.Name
				provider.Namespace = v.Namespace
				provider.Type = v.Spec.Type
			}
		}
	}
	return nil
}

func (r *MariaDBConsumerReconciler) patchFailureStatus(
	ctx context.Context,
	mariaDBConsumer *mariadbv1.MariaDBConsumer,
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
		r.Log.WithValues("mariadbconsumer", types.NamespacedName{
			Name:      mariaDBConsumer.ObjectMeta.Name,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		}).Info(fmt.Sprintf("Unable to create mergepatch for %s, error was: %v", mariaDBConsumer.ObjectMeta.Name, err))
		return nil
	}
	if err := r.Patch(ctx, mariaDBConsumer, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		r.Log.WithValues("mariadbconsumer", types.NamespacedName{
			Name:      mariaDBConsumer.ObjectMeta.Name,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		}).Info(fmt.Sprintf("Unable to patch mariadbconsumer %s, error was: %v", mariaDBConsumer.ObjectMeta.Name, err))
		return nil
	}
	r.Log.WithValues("mariadbconsumer", types.NamespacedName{
		Name:      mariaDBConsumer.ObjectMeta.Name,
		Namespace: mariaDBConsumer.ObjectMeta.Namespace,
	}).Info(fmt.Sprintf("Patched mariadbconsumer %s", mariaDBConsumer.ObjectMeta.Name))
	return nil
}
