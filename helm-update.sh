#!/bin/bash

dbaas () {
    pushd charts
    helm package dbaas-operator
    helm repo index .
    popd
}

provider () {
    pushd charts
    helm package mariadbprovider
    helm repo index .
    popd
}

case $1 in
  dbaas)
    dbaas
    ;;
  provider)
    provider
    ;;
  *)
    echo "nothing"
    ;;
esac