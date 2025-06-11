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
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mariadbv1 "github.com/amazeeio/dbaas-operator/apis/mariadb/v1"
	corev1 "k8s.io/api/core/v1"
)

type PasswordSecretRef struct {
	Name string
	Key  string
}

// MariaDBProviderReconciler reconciles a MariaDBProvider object
type MariaDBProviderReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	Environment          string
	Hostname             string
	ReadReplicaHostnames []string
	Password             string
	PasswordSecretRef    *PasswordSecretRef
	Port                 string
	Username             string
	Type                 string
}

// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.amazee.io,resources=mariadbproviders/status,verbs=get;update;patch

// Reconcile .
func (r *MariaDBProviderReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("mariadbprovider", req.NamespacedName)

	var mariaDBProvider mariadbv1.MariaDBProvider
	if err := r.Get(ctx, req.NamespacedName, &mariaDBProvider); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.provider.mariadb.amazee.io/v1"

	// labels := map[string]string{
	// 	LabelAppName: mariaDBProvider.ObjectMeta.Name,
	// 	LabelAppType: "database-provider",
	// }

	var password string
	if mariaDBProvider.Spec.PasswordSecretRef != nil {
		var secret corev1.Secret
		secretName := types.NamespacedName{
			Name:      mariaDBProvider.Spec.PasswordSecretRef.Name,
			Namespace: req.Namespace,
		}
		err := r.Get(ctx, secretName, &secret)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get Secret %s: %w", secretName.Name, err)
		}

		val, ok := secret.Data[mariaDBProvider.Spec.PasswordSecretRef.Key]
		if !ok {
			return ctrl.Result{}, fmt.Errorf("key %s not found in Secret %s", mariaDBProvider.Spec.PasswordSecretRef.Key, secret.Name)
		}
		password = string(val)
	} else {
		password = mariaDBProvider.Spec.Password
	}
	r.Password = password // Optional: make it available on the reconciler

	// examine DeletionTimestamp to determine if object is under deletion
	if mariaDBProvider.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(mariaDBProvider.ObjectMeta.Finalizers, finalizerName) {
			mariaDBProvider.ObjectMeta.Finalizers = append(mariaDBProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &mariaDBProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(mariaDBProvider.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&mariaDBProvider, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			mariaDBProvider.ObjectMeta.Finalizers = removeString(mariaDBProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &mariaDBProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager .
func (r *MariaDBProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.MariaDBProvider{}).
		Complete(r)
}

func (r *MariaDBProviderReconciler) deleteExternalResources(mariaDBProvider *mariadbv1.MariaDBProvider, namespace string) error {
	//
	// delete any external resources associated with the mariaDBProvider
	//
	return nil
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

func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
