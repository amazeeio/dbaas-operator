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

const (
	// LabelAppName for discovery.
	LabelAppName = "app_name"
	// LabelAppType for discovery.
	LabelAppType = "app_type"
)

// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbconsumers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbproviders,verbs=get;list
// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbconsumers/status,verbs=get;update;patch

// Reconcile .
func (r *MariaDBConsumerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("mariadbconsumer", req.NamespacedName)

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
			fmt.Println("we need a database, continue!")
		} else {
			// drop out if we have all the options already
			return ctrl.Result{}, nil
		}

		// check the providers we have to see who is busy
		provider := &MariaDBPRoviderInfo{}
		if err := r.checkMariaDBProviders(provider, &mariaDBConsumer); err != nil {
			return ctrl.Result{}, err
		}
		if provider.Hostname == "" {
			return ctrl.Result{}, errors.New("No suitable database servers")
		}

		if err := createDatabaseIfNotExist(provider.Hostname, provider.Username, provider.Password, provider.Port, mariaDBConsumer.Spec.Database); err != nil {
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
			Name:      "lagoon-env",
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
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
			Data: map[string]string{"INIT_EMPTY": "yes"},
		}

		err = r.Get(context.TODO(), namespacedName, configMap)
		if err != nil {
			// configMap.Data = newVars
			if err := r.Create(context.Background(), configMap); err != nil {
				return ctrl.Result{}, err
			}
		}
		for mapKey, newVar := range newVars {
			configMap.Data[mapKey] = newVar
		}
		if err := r.Update(context.Background(), configMap); err != nil {
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
		return errors.New("unable to determine server to deprovision from")
	}
	err := dropDatabase(provider.Hostname, provider.Username, provider.Password, provider.Port, mariaDBConsumer.Spec.Database)
	if err != nil {
		return err
	}
	// Delete the primary
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariaDBConsumer.Spec.ServiceHostname,
			Namespace: mariaDBConsumer.ObjectMeta.Namespace,
		},
	}
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
	if err := r.Delete(context.TODO(), serviceRR); ignoreNotFound(err) != nil {
		return err
	}

	// Remove our variables from the lagoon-env configmap
	configMap := &corev1.ConfigMap{}
	// get the current configmap
	err = r.Get(context.TODO(), types.NamespacedName{Namespace: mariaDBConsumer.ObjectMeta.Namespace, Name: "lagoon-env"}, configMap)
	if err != nil {
		return err
	}
	// delete vars from the configmap
	varNames := []string{"DB_TYPE", "DB_NAME", "DB_HOST", "DB_READREPLICA_HOSTS", "DB_PORT", "DB_USER", "DB_PASSWORD"}
	for _, varName := range varNames {
		delete(configMap.Data, varName)
	}
	// update the configmap
	if err := r.Update(context.Background(), configMap); err != nil {
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

func createDatabaseIfNotExist(hostname string, username string, password string, port string, name string) error {
	db, err := sql.Open("mysql", username+":"+password+"@tcp("+hostname+":"+port+")/")
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS " + name)
	if err != nil {
		return err
	}
	return nil
}

func dropDatabase(hostname string, username string, password string, port string, name string) error {
	db, err := sql.Open("mysql", username+":"+password+"@tcp("+hostname+":"+port+")/")
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("DROP DATABASE " + name)
	if err != nil {
		return err
	}
	return nil
}

// check the status of the mariadb provider and return true/false if we can use it
func checkMariaDBStatus(hostname string, username string, password string, port string) (bool, error) {
	db, err := sql.Open("mysql", username+":"+password+"@tcp("+hostname+":"+port+")/")
	if err != nil {
		return false, err
	}
	defer db.Close()

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
	result, err := db.Query("SELECT COUNT(*) FROM information_schema.SCHEMATA")
	if err != nil {
		return false, err
	}
	for result.Next() {
		var value int
		err = result.Scan(&value)
		if err != nil {
			return false, err
		}
		fmt.Println("database count on", hostname, port, "is", value)
		if value > 10 {
			return false, nil
		}
	}
	return true, nil
}

// grab all the MariaDBProvider kinds and check each one
func (r *MariaDBConsumerReconciler) checkMariaDBProviders(provider *MariaDBPRoviderInfo, mariaDBConsumer *mariadbv1.MariaDBConsumer) error {
	providers := &mariadbv1.MariaDBProviderList{}
	src := providers.DeepCopyObject()
	if err := r.List(context.TODO(), src.(*mariadbv1.MariaDBProviderList)); err != nil {
		return err
	}
	providersList := src.(*mariadbv1.MariaDBProviderList)
	for _, v := range providersList.Items {
		if v.Spec.Environment == mariaDBConsumer.Spec.Environment {
			useMe, err := checkMariaDBStatus(v.Spec.Hostname, v.Spec.Username, v.Spec.Password, v.Spec.Port)
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
