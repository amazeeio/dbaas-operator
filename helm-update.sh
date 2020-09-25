#!/bin/bash

# ./helm-update.sh index
#   create new index for the chart

# ./helm-update.sh template
#   process the chart to a template

# ./helm-update.sh delete
#   delete the chart from kubernetes

# ./helm-update.sh install
#   install the chart into kubernetes

# ./helm-update.sh install-tgz
#   install the chart from one of the tgz files present locally into kubernetes

case $1 in
  index)
    pushd charts
    helm package dbaas-operator
    helm repo index .
    popd
    ;;
  index-psql)
    pushd charts
    helm package postgresqlprovider
    helm repo index .
    popd
    ;;
  index-msql)
    pushd charts
    helm package mariadbprovider
    helm repo index .
    popd
    ;;
  template)
    helm template charts/dbaas-operator -f charts/dbaas-operator/values.yaml
    ;;
  delete)
    helm delete -n dbaas-operator dbaas-operator
    ;;
  install)
    helm repo add dbaas-operator https://raw.githubusercontent.com/amazeeio/dbaas-operator/master/charts
    helm upgrade --install -n dbaas-operator dbaas-operator dbaas-operator/dbaas-operator
    ;;
  install-tgz)
    options=($(ls charts | grep tgz))
    if [ ${#options[@]} -ne 0 ]; then
      select chart in "${options[@]}";
      do
        case $chart in
              "$QUIT")
                echo "Unknown option, exiting."
                break
                ;;
              *)
                break
                ;;
        esac
      done
      if [ "$chart" != "" ]; then
        helm upgrade --install -n dbaas-operator dbaas-operator charts/$chart
      fi
    else
      echo "No chart files, exiting."
    fi
    ;;
  *)
    echo "nothing"
    ;;
esac
