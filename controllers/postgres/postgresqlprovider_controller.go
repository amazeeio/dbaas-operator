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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	postgresv1 "github.com/amazeeio/dbaas-operator/apis/postgres/v1"
)

// PostgreSQLProviderReconciler reconciles a PostgreSQLProvider object
type PostgreSQLProviderReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=postgres.amazee.io,resources=postgresqlproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgres.amazee.io,resources=postgresqlproviders/status,verbs=get;update;patch

func (r *PostgreSQLProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// ctx := context.Background()
	_ = r.Log.WithValues("postgresqlprovider", req.NamespacedName)

	var postgresqlProvider postgresv1.PostgreSQLProvider
	if err := r.Get(ctx, req.NamespacedName, &postgresqlProvider); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.provider.postgres.amazee.io/v1"

	// examine DeletionTimestamp to determine if object is under deletion
	if postgresqlProvider.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(postgresqlProvider.ObjectMeta.Finalizers, finalizerName) {
			postgresqlProvider.ObjectMeta.Finalizers = append(postgresqlProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &postgresqlProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(postgresqlProvider.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&postgresqlProvider, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			postgresqlProvider.ObjectMeta.Finalizers = removeString(postgresqlProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &postgresqlProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *PostgreSQLProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1.PostgreSQLProvider{}).
		Complete(r)
}

func (r *PostgreSQLProviderReconciler) deleteExternalResources(postgresqlProvider *postgresv1.PostgreSQLProvider, namespace string) error {
	//
	// delete any external resources associated with the postgresqlProvider
	//
	return nil
}
