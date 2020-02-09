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
	"errors"
	"fmt"
	"math/rand"
	"strings"

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
	Secret                     string
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

// MariaDBPRoviderInfo .
type MariaDBPRoviderInfo struct {
	Hostname             string
	ReadReplicaHostnames []string
	Username             string
	Password             string
	Port                 string
	Name                 string
	Namespace            string
}

// MariaDBConsumerInfo .
type MariaDBConsumerInfo struct {
	DatabaseName string
	Username     string
	Password     string
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=list;get;watch;create;update;patch;delete

// Reconcile .
func (r *MariaDBConsumerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
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

	// examine DeletionTimestamp to determine if object is under deletion
	if mariaDBConsumer.ObjectMeta.DeletionTimestamp.IsZero() {
		// set up the new credentials
		if mariaDBConsumer.Spec.Secret == "" {
			mariaDBConsumer.Spec.Secret = req.Name + "-credentials-" + randSeq(5, true)
		}
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
			mariaDBConsumer.Spec.Consumer.Services.Primary = truncateString(req.Name, 27) + "-" + uuid.New().String()
		}

		provider := &MariaDBPRoviderInfo{}
		// if we haven't got any provider specific information pre-defined, we should query the providers to get one
		if mariaDBConsumer.Spec.Provider.Hostname == "" || mariaDBConsumer.Spec.Provider.Port == "" || len(mariaDBConsumer.Spec.Provider.ReadReplicaHostnames) == 0 {
			opLog.Info(fmt.Sprintf("Attempting to create database %s on any usable mariadb provider", mariaDBConsumer.Spec.Consumer.Database))
			// check the providers we have to see who is busy
			if err := r.checkMariaDBProviders(provider, &mariaDBConsumer, req.NamespacedName); err != nil {
				return ctrl.Result{}, err
			}
			if provider.Hostname == "" {
				opLog.Info("No suitable mariadb providers found, bailing")
				return ctrl.Result{}, nil
			}

			consumer := MariaDBConsumerInfo{
				DatabaseName: mariaDBConsumer.Spec.Consumer.Database,
				Username:     mariaDBConsumer.Spec.Consumer.Username,
				Password:     mariaDBConsumer.Spec.Consumer.Password,
			}

			if err := createDatabaseIfNotExist(*provider, consumer); err != nil {
				return ctrl.Result{}, err
			}

			// populate with provider host information. we don't expose provider credentials here
			if mariaDBConsumer.Spec.Provider.Hostname == "" {
				mariaDBConsumer.Spec.Provider.Hostname = provider.Hostname
			}
			if mariaDBConsumer.Spec.Provider.Port == "" {
				mariaDBConsumer.Spec.Provider.Port = provider.Port
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
				return ctrl.Result{}, err
			}
		}
		if err := r.Update(context.Background(), service); err != nil {
			return ctrl.Result{}, err
		}
		// check if read replica service exists, get if it does, create otherwise
		if len(mariaDBConsumer.Spec.Consumer.Services.Replicas) == 0 {
			for _, replica := range mariaDBConsumer.Spec.Provider.ReadReplicaHostnames {
				replicaName := truncateString("readreplica-"+req.Name, 27) + "-" + uuid.New().String()
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
						return ctrl.Result{}, err
					}
				}
				if err := r.Update(context.Background(), serviceRR); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		// check if the secret for this consumer exists, get if it does, create otherwise
		namespacedName := types.NamespacedName{
			Namespace: req.Namespace,
			Name:      mariaDBConsumer.Spec.Secret,
		}
		replicas := strings.Join(mariaDBConsumer.Spec.Consumer.Services.Replicas[:], ",")
		newVars := map[string]string{
			"DB_TYPE":              "mariadb",
			"DB_NAME":              mariaDBConsumer.Spec.Consumer.Database,
			"DB_HOST":              mariaDBConsumer.Spec.Consumer.Services.Primary,
			"DB_READREPLICA_HOSTS": replicas,
			"DB_PORT":              mariaDBConsumer.Spec.Provider.Port,
			"DB_USER":              mariaDBConsumer.Spec.Consumer.Username,
			"DB_PASSWORD":          mariaDBConsumer.Spec.Consumer.Password,
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Labels:    labels,
				Namespace: namespacedName.Namespace,
			},
			Data: map[string][]byte{},
		}
		for mapKey, newVar := range newVars {
			secret.Data[mapKey] = []byte(newVar)
		}

		err = r.Get(context.TODO(), namespacedName, secret)
		if err != nil {
			opLog.Info(fmt.Sprintf("Creating secret %s in namespace %s", namespacedName.Name, mariaDBConsumer.ObjectMeta.Namespace))
			if err := r.Create(context.Background(), secret); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Update(context.Background(), secret); err != nil {
			return ctrl.Result{}, err
		}

		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName) {
			mariaDBConsumer.ObjectMeta.Finalizers = append(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &mariaDBConsumer); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(mariaDBConsumer.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&mariaDBConsumer, req.NamespacedName.Namespace); err != nil {
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

	return ctrl.Result{}, nil
}

// SetupWithManager .
func (r *MariaDBConsumerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.MariaDBConsumer{}).
		Complete(r)
}

func (r *MariaDBConsumerReconciler) deleteExternalResources(mariaDBConsumer *mariadbv1.MariaDBConsumer, namespace string) error {
	opLog := r.Log.WithValues("mariadbconsumer", namespace)
	//
	// delete any external resources associated with the mariadbconsumer
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object.
	// check the providers we have to see who is busy
	provider := &MariaDBPRoviderInfo{}
	if err := r.getMariaDBProvider(provider, mariaDBConsumer); err != nil {
		return err
	}
	if provider.Hostname == "" {
		return errors.New("Unable to determine server to deprovision from")
	}
	consumer := MariaDBConsumerInfo{
		DatabaseName: mariaDBConsumer.Spec.Consumer.Database,
		Username:     mariaDBConsumer.Spec.Consumer.Username,
		Password:     mariaDBConsumer.Spec.Consumer.Password,
	}
	opLog.Info(fmt.Sprintf("Dropping database %s on host %s - %s/%s", consumer.DatabaseName, provider.Hostname, provider.Namespace, provider.Name))
	if err := dropDatabase(*provider, consumer); err != nil {
		return err
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
		return err
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
			return err
		}
	}
	// Delete the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariaDBConsumer.Spec.Secret,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting secret %s in namespace %s", secret.ObjectMeta.Name, secret.ObjectMeta.Namespace))
	if err := r.Delete(context.TODO(), secret); ignoreNotFound(err) != nil {
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

func createDatabaseIfNotExist(provider MariaDBPRoviderInfo, consumer MariaDBConsumerInfo) error {
	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS `" + consumer.DatabaseName + "`;")
	if err != nil {
		return err
	}
	_, err = db.Exec("CREATE USER IF NOT EXISTS `" + consumer.Username + "`@'%' IDENTIFIED BY '" + consumer.Password + "';")
	if err != nil {
		return err
	}
	_, err = db.Exec("GRANT ALL ON `" + consumer.DatabaseName + "`.* TO `" + consumer.Username + "`@'%';")
	if err != nil {
		return err
	}
	_, err = db.Exec("FLUSH PRIVILEGES;")
	if err != nil {
		return err
	}
	return nil
}

func dropDatabase(provider MariaDBPRoviderInfo, consumer MariaDBConsumerInfo) error {
	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("DROP DATABASE `" + consumer.DatabaseName + "`;")
	if err != nil {
		return err
	}
	// delete db user
	_, err = db.Exec("DROP USER `" + consumer.Username + "`;")
	if err != nil {
		return err
	}
	_, err = db.Exec("FLUSH PRIVILEGES;")
	if err != nil {
		return err
	}
	return nil
}

// check the usage of the mariadb provider and return true/false if we can use it
func getMariaDBUsage(provider MariaDBPRoviderInfo, opLog logr.Logger) (MariaDBUsage, error) {
	currentUsage := MariaDBUsage{
		SchemaCount: 0,
		TableCount:  0,
	}

	db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
	if err != nil {
		return currentUsage, err
	}
	defer db.Close()

	result, err := db.Query(`SELECT COUNT(1) AS TableCount
                            FROM information_schema.tables
                            WHERE table_schema NOT IN ('information_schema','mysql', 'sys');`)
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
	                        WHERE schema_name NOT IN ('information_schema','mysql', 'sys');`)
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

// Grab all the MariaDBProvider kinds and check each one
func (r *MariaDBConsumerReconciler) checkMariaDBProviders(provider *MariaDBPRoviderInfo, mariaDBConsumer *mariadbv1.MariaDBConsumer, namespaceName types.NamespacedName) error {
	opLog := r.Log.WithValues("mariadbconsumer", namespaceName)
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return err
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
		if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
			// Form a temporary connection object.
			mDBProvider := MariaDBPRoviderInfo{
				Hostname:  v.Spec.Hostname,
				Username:  v.Spec.Username,
				Password:  v.Spec.Password,
				Port:      v.Spec.Port,
				Name:      v.Name,
				Namespace: v.Namespace,
			}
			currentUsage, err := getMariaDBUsage(mDBProvider, r.Log.WithValues("mariadbconsumer", namespaceName))
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
			if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
				if bestHostname == v.Spec.Hostname {
					provider.Hostname = v.Spec.Hostname
					provider.ReadReplicaHostnames = v.Spec.ReadReplicaHostnames
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

// get info for just one of the providers
func (r *MariaDBConsumerReconciler) getMariaDBProvider(provider *MariaDBPRoviderInfo, mariaDBConsumer *mariadbv1.MariaDBConsumer) error {
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return err
	}
	providersList := src.(*mariadbv1.MariaDBProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Hostname == mariaDBConsumer.Spec.Provider.Hostname && v.Spec.Port == mariaDBConsumer.Spec.Provider.Port {
			provider.Hostname = v.Spec.Hostname
			provider.ReadReplicaHostnames = v.Spec.ReadReplicaHostnames
			provider.Username = v.Spec.Username
			provider.Password = v.Spec.Password
			provider.Port = v.Spec.Port
			provider.Name = v.Name
			provider.Namespace = v.Namespace
		}
	}
	return nil
}
