# DBaaS Operator

This set of controllers is meant to be used as a replacement for the ansible service broker and https://github.com/amazeeio/dbaas-mariadb-apb to run in Kubernetes.

It allows for provisiong and deprovisioning of shared MySQL/MariaDB, PostgreSQL, and MongoDB databases.

## Components
### MariaDB/MySQL
* MariaDBProvider - These contain the core connection details for a specific provider (AWS RDS, Azure DB for MySQL, etc)
* MariaDBConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

### PostgreSQL
* PostgreSQLProvider - These contain the core connection details for a specific provider (AWS RDS primarily or generic postgresql, other cloud providers currently untested)
* PostgreSQLConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

### MongoDB
* MongoDBProvider - These contain the core connection details for a specific provider (Works with AWS DocumentDB, or generic MongoDB with authentication)
* MongoDBConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

## Test It Out
This will spin up a local mysql provider, and then start kind and installs the operator and runs some basic tests to confirm the operation of the operator

### Using circleci locally
Install the `circleci` tool locally and run the following

```
make local-circle
# or
circleci build -v $(pwd):/workdir
```

### Running the tests directly
```
make operator-test
# if at anypoint you need to clean up
make clean
```

## Code references

* Most of the logic for the controllers located in `controllers/<mariadb/postgres/mongodb>/`
* Spec definitions are in `api/<mariadb/postgres/mongodb>/v*/*_types.go`

## Config samples

* located in `config/samples`

# Updating Helm Charts

* Update Helmchart and increase version in `Chart.yaml` and `values.yaml` as required
* run `helm package charts/dbaas-operator -d charts/`
* run `helm package charts/mariadbprovider -d charts/`
* run `helm package charts/postgresqlprovider -d charts/`
* run `helm package charts/mongodbprovider -d charts/`
* run `helm repo index charts`

# Developing
## Install Kubebuilder
```
os=$(go env GOOS)
arch=$(go env GOARCH)

# download kubebuilder and extract it to tmp
curl -L https://go.kubebuilder.io/dl/2.2.0/${os}/${arch} | tar -xz -C /tmp/

# move to a long-term location and put it on your path
# (you'll need to set the KUBEBUILDER_ASSETS env var if you put it somewhere else)
sudo mv /tmp/kubebuilder_2.2.0_${os}_${arch} /usr/local/kubebuilder
export PATH=$PATH:/usr/local/kubebuilder/bin
```

# Other
## Dashboard
```
kubectl apply -f https://raw.githubusercontent.com/kubernetes/dashboard/v2.0.0-beta6/aio/deploy/recommended.yaml
kubectl apply -f test-resources/dashboard-rbac.yaml
kubectl -n kubernetes-dashboard describe secret $(kubectl -n kubernetes-dashboard get secret | grep admin-user | awk '{print $1}')
kubectl proxy
```