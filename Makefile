# Makefile
CLUSTER_NAME := topology-cluster

.PHONY: all check-deps create-cluster label-nodes deploy-demo clean

# Running 'make' will execute the entire setup pipeline
all: check-deps create-cluster label-nodes

check-deps:
	@echo "Checking dependencies..."
	@command -v docker >/dev/null 2>&1 || { echo >&2 "Docker is not installed. Aborting."; exit 1; }
	@command -v kind >/dev/null 2>&1 || { echo >&2 "kind is not installed. Aborting."; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is not installed. Aborting."; exit 1; }

create-cluster:
	@echo "Creating Kind cluster..."
	kind create cluster --name $(CLUSTER_NAME) --config kind-config.yaml --wait 5m

label-nodes:
	@echo "Applying enhanced topology labels for scheduler testing..."
	# Multi-region topology: us-east-1 and us-west-2
	kubectl label node $(CLUSTER_NAME)-worker topology.kubernetes.io/region=us-east-1 topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite
	kubectl label node $(CLUSTER_NAME)-worker2 topology.kubernetes.io/region=us-east-1 topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite
	kubectl label node $(CLUSTER_NAME)-worker3 topology.kubernetes.io/region=us-east-1 topology.kubernetes.io/zone=us-east-1b topology.kubernetes.io/rack=rack-2 --overwrite

deploy-demo:
	@echo "Deploying the Emojivoto demo application..."
	kubectl apply -f https://run.linkerd.io/emojivoto.yml
	@echo "Giving Kubernetes a moment to create the pods..."
	sleep 5
	@echo "Waiting for pods to be ready..."
	kubectl wait --for=condition=ready pod --all -n emojivoto --timeout=90s

scale-apps:
	@echo "Scaling Emojivoto replicas for a larger traffic graph..."
	kubectl scale deploy web --replicas=1 -n emojivoto
	kubectl scale deploy vote-bot --replicas=1 -n emojivoto
	kubectl scale deploy voting --replicas=1 -n emojivoto
	kubectl scale deploy emoji --replicas=1 -n emojivoto

loadgen:
	@echo "Deploying traffic generator to amplify mesh traffic..."
	kubectl apply -f loadgen-deployment.yaml

generate-graph:
	@echo "Generating topology_graph.json from Prometheus..."
	@kubectl port-forward svc/prometheus 9090:9090 -n linkerd-viz >/tmp/prometheus-forward.log 2>&1 &
	@PID=$$!; sleep 5; python3 gen_graph.py; kill $$PID 2>/dev/null || true; wait $$PID 2>/dev/null || true

deploy-boutique:
	@echo "Deploying the Online Boutique demo application..."
	kubectl apply -f https://raw.githubusercontent.com/GoogleCloudPlatform/microservices-demo/main/release/kubernetes-manifests.yaml

	@echo "Waiting for deployments to be ready..."
	kubectl get deployments -o name | xargs -I {} kubectl rollout status {} --timeout=300s

patch-resources:
	@echo "Patching memory limits for resource-constrained services..."
	kubectl set resources deployment emailservice --limits=memory=1Gi --requests=memory=256Mi -n default
	kubectl set resources deployment recommendationservice --limits=memory=1Gi --requests=memory=512Mi -n default
	kubectl set resources deployment adservice --limits=memory=1Gi --requests=memory=512Mi -n default
	kubectl set resources deployment currencyservice --limits=memory=1Gi --requests=memory=256Mi -n default
	kubectl set resources deployment cartservice --limits=memory=1Gi --requests=memory=256Mi -n default
	kubectl set resources deployment paymentservice --limits=memory=1Gi --requests=memory=256Mi -n default

scale-boutique:
	@echo "Scaling Online Boutique replicas for a larger traffic graph..."
	kubectl scale deploy frontend --replicas=5 -n default
	kubectl scale deploy loadgenerator --replicas=5 -n default
	kubectl scale deploy adservice --replicas=3 -n default
	kubectl scale deploy cartservice --replicas=3 -n default
	kubectl scale deploy checkoutservice --replicas=3 -n default
	kubectl scale deploy currencyservice --replicas=3 -n default
	kubectl scale deploy emailservice --replicas=3 -n default
	kubectl scale deploy paymentservice --replicas=3 -n default
	kubectl scale deploy productcatalogservice --replicas=3 -n default
	kubectl scale deploy recommendationservice --replicas=3 -n default
	kubectl scale deploy shippingservice --replicas=3 -n default

loadgen-boutique:
	@echo "The Online Boutique already includes a loadgenerator deployment..."
	@echo "To increase traffic, scale the loadgenerator deployment using 'make scale-boutique'"

clean:
	@echo "Cleaning up: Deleting Kind cluster..."
	kind delete cluster --name $(CLUSTER_NAME)