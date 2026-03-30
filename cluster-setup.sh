#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

CLUSTER_NAME="topo-cluster"

echo "🚀 Step 1: Creating Kind cluster..."
kind create cluster --name $CLUSTER_NAME --config kind-config.yaml

echo "🏷️ Step 2: Applying mock topology labels for scheduler testing..."
# Simulating Rack 1 in AZ a
kubectl label node ${CLUSTER_NAME}-worker topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite
kubectl label node ${CLUSTER_NAME}-worker2 topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite

# Simulating Rack 2 in AZ b
kubectl label node ${CLUSTER_NAME}-worker3 topology.kubernetes.io/zone=us-east-1b topology.kubernetes.io/rack=rack-2 --overwrite

echo "📦 Step 3: Deploying the Emojivoto demo application..."
kubectl apply -f https://run.linkerd.io/emojivoto.yml

echo "⏳ Waiting for deployments to be available..."
kubectl wait --for=condition=available deployment --all -n emojivoto --timeout=90s

echo "✅ Cluster setup complete! Your topology and traffic generator are running."