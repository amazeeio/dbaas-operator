#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.0
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=dbaas-operator-test
OPERATOR_IMAGE=amazeeio/dbaas-operator:test-tag
OPERATOR_NAMESPACE=dbaas-operator-system
CHECK_TIMEOUT=20

check_operator_log () {
  echo "=========== OPERATOR LOG ============"
  kubectl logs $(kubectl get pods  -n ${OPERATOR_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${OPERATOR_NAMESPACE}
}

tear_down () {
  echo "============= TEAR DOWN ============="
  kind delete cluster --name ${KIND_NAME}
  docker-compose down
}

start_up () {
  echo "================ BEGIN ================"
  echo "==> Bring up local provider"
  docker-compose up -d
  CHECK_COUNTER=1
  echo "==> Ensure mariadb database provider is running"
  mariadb_start_check
  echo "==> Ensure postgres database provider is running"
  postgres_start_check
  echo "==> Ensure mongodb database provider is running"
  mongodb_start_check
}

mongodb_start_check () {
  until $(docker run -it mongo mongo mongodb://root:password@mongodb.172.17.0.1.xip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q admin)
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
}

mariadb_start_check () {
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
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
}

postgres_start_check () {
  until $(docker run -it -e PGPASSWORD=password postgres psql -h postgres.172.17.0.1.xip.io -p 5432 -U postgres postgres -c "SELECT datname FROM pg_database" | grep -q "postgres")
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
}

start_kind () {
  echo "==> Start kind ${KIND_VER}" 
  kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME}
  kubectl cluster-info --context kind-${KIND_NAME}

  echo "==> Switch kube context to kind" 
  kubectl config use-context kind-${KIND_NAME}
}

build_deploy_operator () {
  echo "==> Build and deploy operator"
  make test
  make docker-build IMG=${OPERATOR_IMAGE}
  kind load docker-image ${OPERATOR_IMAGE} --name ${KIND_NAME}
  make deploy IMG=${OPERATOR_IMAGE}

  CHECK_COUNTER=1
  echo "==> Ensure operator is running"
  until $(kubectl get pods  -n ${OPERATOR_NAMESPACE} --no-headers | grep -q "Running")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Operator not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
    check_operator_log
    exit 1
  fi
  done

}

check_services () {
  PRIMARY=$(kubectl get $2/$1 -o json | jq -r '.spec.consumer.services.primary')
  echo "====> Check primary service ${PRIMARY}"
  kubectl get service/${PRIMARY} -o yaml
}


check_services_replicas () {
  REPLICAS=$(kubectl get $2/$1 -o json | jq -r '.spec.consumer.services.replicas | .[]')
  for REPLICA in ${REPLICAS}
  do
  echo "====> Check replica service ${REPLICA}"
    kubectl get service/${REPLICA} -o yaml
  done
}

add_delete_consumer_mariadb () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get mariadbconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    exit 1
  fi
  done
  echo "====> Get MariaDBConsumer"
  kubectl get mariadbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mariadbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_EXISTS} exists"
  else 
    echo "database ${DB_EXISTS} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2 mariadbconsumer
  check_services_replicas $2 mariadbconsumer

  echo "==> Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then 
    echo "failed to delete consumer"
    check_operator_log
    exit 1
  fi
  echo "==> Check if the operator deletes the database"
  DB_EXISTS=$(docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -qfsBNe "SELECT schema_name FROM information_schema.schemata WHERE schema_name = '${DB_NAME}';")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_EXISTS} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_EXISTS} does not exist"
  fi
}


add_delete_consumer_psql () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get postgresqlconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    exit 1
  fi
  done
  echo "====> Get PostgreSQLConsumer"
  kubectl get postgresqlconsumer/$2 -o yaml
  DB_NAME=$(kubectl get postgresqlconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  DB_EXISTS=$(docker run -it -e PGPASSWORD=password postgres psql -h postgres.172.17.0.1.xip.io -p 5432 -U postgres postgres --no-align --tuples-only -c "SELECT datname FROM pg_database;" | grep -q "${DB_NAME}")
  if [[ -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2 postgresqlconsumer

  echo "==> Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then
    echo "failed to delete consumer"
    check_operator_log
    exit 1
  fi
  echo "==> Check if the operator deletes the database"
  DB_EXISTS=$(docker run -it -e PGPASSWORD=password postgres psql -h postgres.172.17.0.1.xip.io -p 5432 -U postgres postgres --no-align --tuples-only -c "SELECT datname FROM pg_database;" | grep -q "${DB_NAME}")
  if [[ ! -z "${DB_EXISTS}" ]]
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

add_delete_consumer_mongodb () {
  echo "====> Add a consumer"
  kubectl apply -f $1
  CHECK_COUNTER=1
  until kubectl get mongodbconsumer/$2 -o json | jq -e '.spec.consumer.database?'
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database not created yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database creation reached"
    check_operator_log
    exit 1
  fi
  done
  echo "====> Get MongoDBConsumer"
  kubectl get mongodbconsumer/$2 -o yaml
  DB_NAME=$(kubectl get mongodbconsumer/$2 -o json | jq -r '.spec.consumer.database')
  echo "==> Check if the operator creates the database"
  if docker run -it mongo mongo mongodb://root:password@mongodb.172.17.0.1.xip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
  else 
    echo "database ${DB_NAME} does not exist"
    check_operator_log
    exit 1
  fi

  echo "==> Check services"
  check_services $2 mongodbconsumer

  echo "==> Delete the consumer"
  timeout 60 kubectl delete -f $1
  if [ $? -ne 0 ]
  then
    echo "failed to delete consumer"
    check_operator_log
    exit 1
  fi
  echo "==> Check if the operator deletes the database"
  if docker run -it mongo mongo mongodb://root:password@mongodb.172.17.0.1.xip.io:27017/  --quiet --eval 'db.getMongo().getDBNames().forEach(function(db){print(db)})' | grep -q "${DB_NAME}"
  then 
    echo "database ${DB_NAME} exists"
    check_operator_log
    exit 1
  else 
    echo "database ${DB_NAME} does not exist"
  fi
}

start_up
start_kind
build_deploy_operator

echo "=> Test MariaDB"
echo "==> Add a provider"
kubectl apply -f test-resources/mariadb-provider.yaml
kubectl get mariadbprovider/mariadbprovider-testing -o yaml

echo "==> Test blank consumer"
add_delete_consumer_mariadb test-resources/mariadb-consumer.yaml mariadbconsumer-testing
 
echo "==> Test seeded consumer"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb; CREATE USER IF NOT EXISTS testdb@'%' IDENTIFIED BY 'testdb'; GRANT ALL ON testdb.* TO testdb@'%'; FLUSH PRIVILEGES;"
add_delete_consumer_mariadb test-resources/mariadb-consumer-test.yaml mariadbconsumer-testing-2

echo "==> Test seeded consumer V2"
docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e "CREATE DATABASE IF NOT EXISTS testdb1; CREATE USER IF NOT EXISTS testdb1@'%' IDENTIFIED BY 'testdb1'; GRANT ALL ON testdb1.* TO testdb1@'%'; FLUSH PRIVILEGES;"
add_delete_consumer_mariadb test-resources/mariadb-consumer-test-2.yaml mariadbconsumer-testing-3

echo "=> Test PostgreSQL"
echo "==> Add a provider"
kubectl apply -f test-resources/psql-provider.yaml
kubectl get postgresqlprovider/postgreprovider-testing -o yaml

echo "==> Test blank consumer"
add_delete_consumer_psql test-resources/psql-consumer.yaml psqlconsumer-testing

echo "=> Test MongoDB"
echo "==> Add a provider"
kubectl apply -f test-resources/mongo-provider.yaml
kubectl get mongodbprovider/mongodbprovider-testing -o yaml

echo "==> Test blank consumer"
add_delete_consumer_mongodb test-resources/mongo-consumer.yaml mongodbconsumer-testing

check_operator_log
tear_down
echo "================ END ================"