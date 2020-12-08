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

	mongodbv1 "github.com/amazeeio/dbaas-operator/apis/mongodb/v1"
)

// MongoDBProviderReconciler reconciles a MongoDBProvider object
type MongoDBProviderReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mongodb.amazee.io,resources=mongodbproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mongodb.amazee.io,resources=mongodbproviders/status,verbs=get;update;patch

// Reconcile .
func (r *MongoDBProviderReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("mongodbprovider", req.NamespacedName)

	var mongodbProvider mongodbv1.MongoDBProvider
	if err := r.Get(ctx, req.NamespacedName, &mongodbProvider); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	// your logic here
	finalizerName := "finalizer.provider.mongodb.amazee.io/v1"

	// examine DeletionTimestamp to determine if object is under deletion
	if mongodbProvider.ObjectMeta.DeletionTimestamp.IsZero() {

		// we we don't get any auth specs defined, then we just set them to be admin/SCRAM-SHA-1
		if mongodbProvider.Spec.Auth.Source == "" {
			mongodbProvider.Spec.Auth.Source = "admin"
		}
		if mongodbProvider.Spec.Auth.Mechanism == "" {
			mongodbProvider.Spec.Auth.Mechanism = "SCRAM-SHA-1"
		}
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(mongodbProvider.ObjectMeta.Finalizers, finalizerName) {
			mongodbProvider.ObjectMeta.Finalizers = append(mongodbProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &mongodbProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(mongodbProvider.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&mongodbProvider, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			mongodbProvider.ObjectMeta.Finalizers = removeString(mongodbProvider.ObjectMeta.Finalizers, finalizerName)
			if err := r.Update(context.Background(), &mongodbProvider); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager .
func (r *MongoDBProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mongodbv1.MongoDBProvider{}).
		Complete(r)
}

func (r *MongoDBProviderReconciler) deleteExternalResources(mongodbProvider *mongodbv1.MongoDBProvider, namespace string) error {
	//
	// delete any external resources associated with the mongodbProvider
	//
	return nil
}
