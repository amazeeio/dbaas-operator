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

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mariadbv1 "github.com/amazeeio/dbaas-operator/api/v1"

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
}

// MariaDBPRoviderInfo .
type MariaDBPRoviderInfo struct {
	Hostname            string
	ReadReplicaHostname string
	Username            string
	Password            string
	Port                string
}

// MariaDBConsumerInfo .
type MariaDBConsumerInfo struct {
	DatabaseName string
	Username     string
	Password     string
}

const (
	// LabelAppName for discovery.
	LabelAppName = "app_name"
	// LabelAppType for discovery.
	LabelAppType = "app_type"
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
		LabelAppName: mariaDBConsumer.ObjectMeta.Name,
		LabelAppType: "mariadb-consumer",
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if mariaDBConsumer.ObjectMeta.DeletionTimestamp.IsZero() {
		// set up the new credentials
		if mariaDBConsumer.Spec.ServiceHostname == "" {
			mariaDBConsumer.Spec.ServiceHostname = "mariadb-" + uuid.New().String()
		}
		if mariaDBConsumer.Spec.Database == "" {
			mariaDBConsumer.Spec.Database = truncateString(req.NamespacedName.Namespace, 50) + "_" + randSeq(5)
		}
		if mariaDBConsumer.Spec.Username == "" {
			mariaDBConsumer.Spec.Username = truncateString(req.NamespacedName.Namespace, 10) + "_" + randSeq(5)
		}
		if mariaDBConsumer.Spec.Password == "" {
			mariaDBConsumer.Spec.Password = randSeq(24)
		}
		if mariaDBConsumer.Spec.ServiceReadReplicaHostname == "" {
			mariaDBConsumer.Spec.ServiceReadReplicaHostname = "mariadb-readreplica-" + uuid.New().String()
		}
		if mariaDBConsumer.Spec.Hostname == "" || mariaDBConsumer.Spec.Port == "" || mariaDBConsumer.Spec.ReadReplicaHostname == "" {
			opLog.Info(fmt.Sprintf("Attempting to create database %s on any usable mariadb provider", mariaDBConsumer.Spec.Database))
		} else {
			// drop out if we have all the options already
			return ctrl.Result{}, nil
		}

		// check the providers we have to see who is busy
		provider := &MariaDBPRoviderInfo{}
		if err := r.checkMariaDBProviders(provider, &mariaDBConsumer, req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}
		if provider.Hostname == "" {
			opLog.Info("No suitable mariadb providers found, bailing")
			return ctrl.Result{}, nil
		}
		consumer := MariaDBConsumerInfo{
			DatabaseName: mariaDBConsumer.Spec.Database,
			Username:     mariaDBConsumer.Spec.Username,
			Password:     mariaDBConsumer.Spec.Password,
		}

		if err := createDatabaseIfNotExist(*provider, consumer); err != nil {
			return ctrl.Result{}, err
		}

		// once we have a provider, update our crd
		if mariaDBConsumer.Spec.Hostname == "" {
			mariaDBConsumer.Spec.Hostname = provider.Hostname
		}
		if mariaDBConsumer.Spec.Port == "" {
			mariaDBConsumer.Spec.Port = provider.Port
		}
		if mariaDBConsumer.Spec.ReadReplicaHostname == "" {
			mariaDBConsumer.Spec.ReadReplicaHostname = provider.ReadReplicaHostname
		}

		// check if service exists, get if it does
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mariaDBConsumer.Spec.ServiceHostname,
				Labels:    labels,
				Namespace: mariaDBConsumer.ObjectMeta.Namespace,
			},
			Spec: corev1.ServiceSpec{
				ExternalName: mariaDBConsumer.Spec.Hostname,
				Type:         corev1.ServiceTypeExternalName,
			},
		}
		err := r.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: service.ObjectMeta.Name}, service)
		if err != nil {
			if err := r.Create(context.Background(), service); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Update(context.Background(), service); err != nil {
			return ctrl.Result{}, err
		}
		// check if service exists, get if it does
		serviceRR := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mariaDBConsumer.Spec.ServiceReadReplicaHostname,
				Labels:    labels,
				Namespace: mariaDBConsumer.ObjectMeta.Namespace,
			},
			Spec: corev1.ServiceSpec{
				ExternalName: mariaDBConsumer.Spec.ReadReplicaHostname,
				Type:         corev1.ServiceTypeExternalName,
			},
		}
		err = r.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: serviceRR.ObjectMeta.Name}, serviceRR)
		if err != nil {
			if err := r.Create(context.Background(), serviceRR); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Update(context.Background(), serviceRR); err != nil {
			return ctrl.Result{}, err
		}
		namespacedName := types.NamespacedName{
			Namespace: req.Namespace,
			Name:      "mariadb-operator-credentials",
		}
		newVars := map[string]string{
			"DB_TYPE":              "mariadb",
			"DB_NAME":              mariaDBConsumer.Spec.Database,
			"DB_HOST":              mariaDBConsumer.Spec.ServiceHostname,
			"DB_READREPLICA_HOSTS": mariaDBConsumer.Spec.ServiceReadReplicaHostname,
			"DB_PORT":              mariaDBConsumer.Spec.Port,
			"DB_USER":              mariaDBConsumer.Spec.Username,
			"DB_PASSWORD":          mariaDBConsumer.Spec.Password,
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
			Data: map[string][]byte{},
		}
		for mapKey, newVar := range newVars {
			secret.Data[mapKey] = []byte(newVar)
		}

		err = r.Get(context.TODO(), namespacedName, secret)
		if err != nil {
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
		DatabaseName: mariaDBConsumer.Spec.Database,
		Username:     mariaDBConsumer.Spec.Username,
		Password:     mariaDBConsumer.Spec.Password,
	}
	opLog.Info(fmt.Sprintf("Dropping database %s on host %s", consumer.DatabaseName, provider.Hostname))
	if err := dropDatabase(*provider, consumer); err != nil {
		return err
	}
	// Delete the primary
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariaDBConsumer.Spec.ServiceHostname,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", service.ObjectMeta.Name, service.ObjectMeta.Namespace))
	if err := r.Delete(context.TODO(), service); ignoreNotFound(err) != nil {
		return err
	}
	// Delete the read replica
	serviceRR := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariaDBConsumer.Spec.ServiceReadReplicaHostname,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		},
	}
	opLog.Info(fmt.Sprintf("Deleting service %s in namespace %s", serviceRR.ObjectMeta.Name, serviceRR.ObjectMeta.Namespace))
	if err := r.Delete(context.TODO(), serviceRR); ignoreNotFound(err) != nil {
		return err
	}
	// Delete the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb-operator-credentials",
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

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
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
	_, err = db.Exec("CREATE USER `" + consumer.Username + "`@'%' IDENTIFIED BY '" + consumer.Password + "';")
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
func checkMariaDBUsage(provider MariaDBPRoviderInfo, opLog logr.Logger) (bool, error) {
	opLog.Info(fmt.Sprintf("Checking usage of database server is not currently configured, proceeding to use %s", provider.Hostname))
	// @TODO: once the logic around determining which database to use if more than one of a specific type is defined in a MariaDBProvider kind
	// @TODO: the commented code in this block can be re-evaluated

	/*
		// db, err := sql.Open("mysql", provider.Username+":"+provider.Password+"@tcp("+provider.Hostname+":"+provider.Port+")/")
		// if err != nil {
		// 	return false, err
		// }
		// defer db.Close()
	*/

	// result, err := db.Query("show status like 'Qcache_queries_in_cache';")
	// result, err := db.Query("show status like 'Uptime_since_flush_status';")
	// if err != nil {
	// 	return false, err
	// }
	// for result.Next() {
	// 	var name, value string
	// 	err = result.Scan(&name, &value)
	// 	if err != nil {
	// 		return false, err
	// 	}
	// 	fmt.Println(name, value)
	// 	if value > "9000" {
	// 		return false, nil
	// 	}
	// }

	/*
		// result, err := db.Query("SELECT COUNT(*) FROM information_schema.SCHEMATA")
		// if err != nil {
		// 	return false, err
		// }
		// for result.Next() {
		// 	var value int
		// 	err = result.Scan(&value)
		// 	if err != nil {
		// 		return false, err
		// 	}
		// 	opLog.Info("Database count on host %s has reached %v", provider.Hostname, value)
		// 	if value > 50 {
		// 		return false, nil
		// 	}
		// }
	*/
	return true, nil
}

// grab all the MariaDBProvider kinds and check each one
func (r *MariaDBConsumerReconciler) checkMariaDBProviders(provider *MariaDBPRoviderInfo, mariaDBConsumer *mariadbv1.MariaDBConsumer, namespaceName types.NamespacedName) error {
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return err
	}
	providersList := src.(*mariadbv1.MariaDBProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
			mDBProvider := MariaDBPRoviderInfo{
				Hostname: v.Spec.Hostname,
				Username: v.Spec.Username,
				Password: v.Spec.Password,
				Port:     v.Spec.Port,
			}
			useMe, err := checkMariaDBUsage(mDBProvider, r.Log.WithValues("mariadbconsumer", namespaceName))
			if err != nil {
				return err
			}
			if useMe {
				provider.Hostname = v.Spec.Hostname
				provider.ReadReplicaHostname = v.Spec.ReadReplicaHostname
				provider.Username = v.Spec.Username
				provider.Password = v.Spec.Password
				provider.Port = v.Spec.Port
				return nil
			}
		}
	}
	return errors.New("no suitable usable database servers")
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
		if v.Spec.Hostname == mariaDBConsumer.Spec.Hostname && v.Spec.Port == mariaDBConsumer.Spec.Port {
			provider.Hostname = v.Spec.Hostname
			provider.ReadReplicaHostname = v.Spec.ReadReplicaHostname
			provider.Username = v.Spec.Username
			provider.Password = v.Spec.Password
			provider.Port = v.Spec.Port
		}
	}
	return nil
}
