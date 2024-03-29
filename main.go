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

package main

import (
	"flag"
	"os"

	mariadbv1 "github.com/amazeeio/dbaas-operator/apis/mariadb/v1"
	mongodbv1 "github.com/amazeeio/dbaas-operator/apis/mongodb/v1"
	postgresv1 "github.com/amazeeio/dbaas-operator/apis/postgres/v1"
	mariadbcontroller "github.com/amazeeio/dbaas-operator/controllers/mariadb"
	mongodbcontroller "github.com/amazeeio/dbaas-operator/controllers/mongodb"
	postgrescontroller "github.com/amazeeio/dbaas-operator/controllers/postgres"

	"github.com/amazeeio/dbaas-operator/handlers"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = mariadbv1.AddToScheme(scheme)
	_ = postgresv1.AddToScheme(scheme)
	_ = mongodbv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableMariaDBProviders bool
	var enableMongoDBProviders bool
	var enablePostreSQLProviders bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableMongoDBProviders, "enable-mongodb-providers", false,
		"Enable MongoDB provider support.")
	flag.BoolVar(&enablePostreSQLProviders, "enable-postgresql-providers", false,
		"Enable PostgreSQL provider support.")
	flag.BoolVar(&enableMariaDBProviders, "enable-mariadb-providers", true,
		"Enable MariaDB/MySQLDB provider support.")
	flag.Parse()

	ctrl.SetLogger(zap.New(func(o *zap.Options) {
		o.Development = true
	}))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "dbaas-operator-leader-election-helper",
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if !enableMariaDBProviders && !enablePostreSQLProviders && !enableMongoDBProviders {
		setupLog.Error(err, "all providers are disabled, you must enable at least one")
		os.Exit(1)
	}

	if enableMariaDBProviders {
		if err = (&mariadbcontroller.MariaDBConsumerReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("MariaDBConsumer"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MariaDBConsumer")
			os.Exit(1)
		}
		if err = (&mariadbcontroller.MariaDBProviderReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("MariaDBProvider"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MariaDBProvider")
			os.Exit(1)
		}
	}
	if enablePostreSQLProviders {
		if err = (&postgrescontroller.PostgreSQLConsumerReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("PostgreSQLConsumer"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "PostgreSQLConsumer")
			os.Exit(1)
		}
		if err = (&postgrescontroller.PostgreSQLProviderReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("PostgreSQLProvider"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "PostgreSQLProvider")
			os.Exit(1)
		}
	}
	if enableMongoDBProviders {
		if err = (&mongodbcontroller.MongoDBConsumerReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("MongoDBConsumer"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBConsumer")
			os.Exit(1)
		}
		if err = (&mongodbcontroller.MongoDBProviderReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("MongoDBProvider"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MongoDBProvider")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	h := &handlers.Client{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers-http").WithName("DBaaS"),
	}
	// +kubebuilder:scaffold:builder
	setupLog.Info("starting http server")
	go handlers.Run(h, setupLog)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
