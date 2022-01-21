package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	"github.com/gorilla/mux"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"

	mariadbv1 "github.com/amazeeio/dbaas-operator/apis/mariadb/v1"
	mongodbv1 "github.com/amazeeio/dbaas-operator/apis/mongodb/v1"
	postgresv1 "github.com/amazeeio/dbaas-operator/apis/postgres/v1"
)

const (
	// ContentType name of the header that defines the format of the reply
	ContentType = "Content-Type"
	// DBaaSEnvironmentType name of the header that contains if this has been served by dbaas
	DBaaSEnvironmentType = "X-DBaaS-Environment-Type"
	// CacheControl name of the header that defines the cache control config
	CacheControl = "Cache-Control"
)

var (
	favicon = "data:image/x-icon;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
)

// Client is the client structure for http handlers.
type Client struct {
	Client ctrlClient.Client
	Log    logr.Logger
	Debug  bool
	// RequestCount    *prometheus.CounterVec
	// RequestDuration *prometheus.HistogramVec
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=7776000")
	fmt.Fprintln(w, fmt.Sprintf("%s\n", favicon))
}

// Run runs the http server.
func Run(h *Client, setupLog logr.Logger) {
	r := mux.NewRouter()
	r.HandleFunc("/favicon.ico", faviconHandler)
	r.HandleFunc("/mariadb/{environment}", h.mariaDBHandler)
	r.HandleFunc("/mongodb/{environment}", h.mongoDBHandler)
	r.HandleFunc("/postgres/{environment}", h.postgresHandler)

	r.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	err := http.ListenAndServe(fmt.Sprintf(":5000"), r)
	if err != nil {
		setupLog.Error(err, "unable to start http server")
		os.Exit(1)
	}
}

func (h *Client) mariaDBHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	environmentType := vars["environment"]

	ctx := context.Background()
	opLog := h.Log.WithValues("dbaas-operator", "mariadb")
	providers := &mariadbv1.MariaDBProviderList{}

	w.Header().Set(ContentType, "application/json")
	w.Header().Set(DBaaSEnvironmentType, environmentType)
	w.Header().Set(CacheControl, "private,no-store")

	if err := h.Client.List(ctx, providers); err != nil {
		opLog.Info(fmt.Sprintf("Unable to get any providers, error was: %s", err.Error()))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"%s"}`, err.Error()))
		return
	}
	hasProvider := false
	for _, provider := range providers.Items {
		if provider.Spec.Environment == environmentType {
			hasProvider = true
		}
	}
	if hasProvider {
		opLog.Info(fmt.Sprintf("Providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, `{"result":{"found":true}}`)
	} else {
		opLog.Info(fmt.Sprintf("No providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"no providers for dbaas environment %s"}`, environmentType))
	}
}

func (h *Client) mongoDBHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	environmentType := vars["environment"]

	ctx := context.Background()
	opLog := h.Log.WithValues("dbaas-operator", "mongodb")
	providers := &mongodbv1.MongoDBProviderList{}

	w.Header().Set(ContentType, "application/json")
	w.Header().Set(DBaaSEnvironmentType, environmentType)
	w.Header().Set(CacheControl, "private,no-store")

	if err := h.Client.List(ctx, providers); err != nil {
		opLog.Info(fmt.Sprintf("Unable to get any providers, error was: %s", err.Error()))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"%s"}`, err.Error()))
		return
	}
	hasProvider := false
	for _, provider := range providers.Items {
		if provider.Spec.Environment == environmentType {
			hasProvider = true
		}
	}
	if hasProvider {
		opLog.Info(fmt.Sprintf("Providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, `{"result":{"found":true}}`)
	} else {
		opLog.Info(fmt.Sprintf("No providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"no providers for dbaas environment %s"}`, environmentType))
	}
}

func (h *Client) postgresHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	environmentType := vars["environment"]

	ctx := context.Background()
	opLog := h.Log.WithValues("dbaas-operator", "postgres")
	providers := &postgresv1.PostgreSQLProviderList{}

	w.Header().Set(ContentType, "application/json")
	w.Header().Set(DBaaSEnvironmentType, environmentType)
	w.Header().Set(CacheControl, "private,no-store")

	if err := h.Client.List(ctx, providers); err != nil {
		opLog.Info(fmt.Sprintf("Unable to get any providers, error was: %s", err.Error()))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"%s"}`, err.Error()))
		return
	}
	hasProvider := false
	for _, provider := range providers.Items {
		if provider.Spec.Environment == environmentType {
			hasProvider = true
		}
	}
	if hasProvider {
		opLog.Info(fmt.Sprintf("Providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, `{"result":{"found":true}}`)
	} else {
		opLog.Info(fmt.Sprintf("No providers for %s exist", environmentType))
		w.WriteHeader(200)
		fmt.Fprintln(w, fmt.Sprintf(`{"result":{"found":false},"error":"no providers for dbaas environment %s"}`, environmentType))
	}
}
