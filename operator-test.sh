#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.0
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=dbaas-operator-test
OPERATOR_IMAGE=controller:unique
CHECK_TIMEOUT=60

echo "================ BEGIN ================"
echo "==> Bring up local provider"
docker-compose up -d

CHECK_COUNTER=1
echo "==> Ensure database provider is running"
until $(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
  let CHECK_COUNTER=CHECK_COUNTER+1
  echo "Database provider not running yet"
  sleep 5
else
  echo "Timeout of $CHECK_TIMEOUT for database provider startup reached"
  exit 1
fi
done

echo "==> Start kind ${KIND_VER}" 
kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME}
kubectl cluster-info --context kind-${KIND_NAME}
echo "==> Switch kube context to kind" 
kubectl config use-context kind-${KIND_NAME}

echo "==> Build and deploy operator"
make docker-build IMG=${OPERATOR_IMAGE}
kind load docker-image ${OPERATOR_IMAGE} --name ${KIND_NAME}
make deploy IMG=${OPERATOR_IMAGE}

CHECK_COUNTER=1
echo "==> Ensure operator is running"
until $(kubectl get pods  -n dbaas-operator-system --no-headers | grep -q "Running")
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
  let CHECK_COUNTER=CHECK_COUNTER+1
  echo "Operator not running yet"
  sleep 5
else
  echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
  exit 1
fi
done

echo "==> Add a provider"
  kubectl apply -f test-resources/provider.yaml 
echo "==> Add a blank consumer"
kubectl apply -f test-resources/consumer.yaml

CHECK_COUNTER=1
until kubectl get secret $(kubectl get mariadbconsumer/mariadbconsumer-testing -o json | jq -r '.spec.secret')
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
  let CHECK_COUNTER=CHECK_COUNTER+1
  echo "Secret not created yet"
  sleep 5
else
  echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
  exit 1
fi
done
echo "====> Get MariaDBConsumer"
kubectl get mariadbconsumer/mariadbconsumer-testing -o yaml --export
DB_NAME=$(kubectl get mariadbconsumer/mariadbconsumer-testing -o json | jq -r '.spec.consumer.database')
echo "==> Check if the operator creates the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
else 
  echo "database ${DB_EXISTS} does not exist"
  exit 1
fi

echo "==> Delete the consumer"
kubectl delete -f test-resources/consumer.yaml
echo "==> Check if the operator deletes the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
  exit 1
else 
  echo "database ${DB_EXISTS} does not exist"
fi
 
echo "==> Test seeded consumer"
echo "====> Add a seeded database"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;"

echo "====> Add a seeded consumer"
kubectl apply -f test-resources/consumer-test.yaml
CHECK_COUNTER=1
until kubectl get secret $(kubectl get mariadbconsumer/mariadbconsumer-testing-2 -o json | jq -r '.spec.secret')
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
  let COUNTER=SERVICE_BROKER_COUNTER+1
  echo "Secret not created yet"
  sleep 5
else
  echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
  exit 1
fi
done
echo "====> Get MariaDBConsumer"
kubectl get mariadbconsumer/mariadbconsumer-testing-2 -o yaml --export
DB_NAME=$(kubectl get mariadbconsumer/mariadbconsumer-testing-2 -o json | jq -r '.spec.consumer.database')
echo "==> Check if the operator creates the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
else 
  echo "database ${DB_EXISTS} does not exist"
  exit 1
fi

echo "==> Delete the consumer"
kubectl delete -f test-resources/consumer-test.yaml
echo "==> Check if the operator deletes the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
  exit 1
else 
  echo "database ${DB_EXISTS} does not exist"
fi

echo "==> Test seeded consumer V2"
echo "====> Add a seeded database"
docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;"

echo "====> Add a seeded consumer"
kubectl apply -f test-resources/consumer-test-2.yaml
CHECK_COUNTER=1
until kubectl get secret $(kubectl get mariadbconsumer/mariadbconsumer-testing-3 -o json | jq -r '.spec.secret')
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
  let CHECK_COUNTER=CHECK_COUNTER+1
  echo "Secret not created yet"
  sleep 5
else
  echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
  exit 1
fi
done
echo "====> Get MariaDBConsumer"
kubectl get mariadbconsumer/mariadbconsumer-testing-3 -o yaml --export
DB_NAME=$(kubectl get mariadbconsumer/mariadbconsumer-testing-3 -o json | jq -r '.spec.consumer.database')
echo "==> Check if the operator creates the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
else 
  echo "database ${DB_EXISTS} does not exist"
  exit 1
fi

echo "==> Delete the consumer"
kubectl delete -f test-resources/consumer-test-2.yaml
echo "==> Check if the operator deletes the database"
DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
if [[ ! -z "${DB_EXISTS}" ]]
then 
  echo "database ${DB_EXISTS} exists"
  exit 1
else 
  echo "database ${DB_EXISTS} does not exist"
fi
echo "=========== OPERATOR LOG ============"
kubectl logs $(kubectl get pods  -n dbaas-operator-system --no-headers | awk '{print $1}') -c manager -n dbaas-operator-system
echo "============= TEAR DOWN ============="
kind delete cluster --name ${KIND_NAME}
docker-compose down
echo "================ END ================"