# DBaaS Operator

This operator is meant to be used as a replacement for the ansible service broker and https://github.com/amazeeio/dbaas-mariadb-apb to run in Kubernetes.

## Components
* MariaDBProvider - These contain the core connection details for a specific provider (AWS RDS, Azure DB for MySQL, etc)
* MariaDBConsumer - These contain the specific connection details for a consumer, multiple consumers can be defined in the one namespace and each will get their own specific service endpoints created pointing to the provider.

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

## Test It Out
This will spin up a local mysql provider, and then start kind and installs the operator and runs some basic tests to confirm the operation of the operator

Using circleci locally
```
circleci build -v $(pwd):/workdir
```

```
make operator-test

# if at anypoint you need to clean up
make clean
```

# Other
## Dashboard
```
kubectl apply -f https://raw.githubusercontent.com/kubernetes/dashboard/v2.0.0-beta6/aio/deploy/recommended.yaml
kubectl apply -f test-resources/dashboard-rbac.yaml
kubectl -n kubernetes-dashboard describe secret $(kubectl -n kubernetes-dashboard get secret | grep admin-user | awk '{print $1}')
kubectl proxy
```

## Code references

* Most of the logic for the operator is in `controllers/`
* Spec definitions are in `api/v1/*_types.go`

## Config samples

* located in `config/samples`

# Update Helm Charts

* Update Helmchart and increase version in `Chart.yaml`
* run `helm package charts/dbaas-operator -d charts/` and `helm package charts/mariadbprovider -d charts/`
* run `helm repo index charts`