# Makefile
CLUSTER_NAME := topo-cluster

.PHONY: all check-deps create-cluster label-nodes deploy-demo clean deep-clean

# Running 'make' will execute the entire setup pipeline
all: check-deps create-cluster label-nodes deploy-demo

check-deps:
	@echo "Checking dependencies..."
	@command -v docker >/dev/null 2>&1 || { echo >&2 "Docker is not installed. Aborting."; exit 1; }
	@command -v kind >/dev/null 2>&1 || { echo >&2 "kind is not installed. Aborting."; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo >&2 "kubectl is not installed. Aborting."; exit 1; }

create-cluster:
	@echo "Creating Kind cluster..."
	kind create cluster --name $(CLUSTER_NAME) --config kind-config.yaml

label-nodes:
	@echo "Applying mock topology labels for scheduler testing..."
	# Simulating Rack 1 in AZ a
	kubectl label node $(CLUSTER_NAME)-worker topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite
	kubectl label node $(CLUSTER_NAME)-worker2 topology.kubernetes.io/zone=us-east-1a topology.kubernetes.io/rack=rack-1 --overwrite
	
	# Simulating Rack 2 in AZ b
	kubectl label node $(CLUSTER_NAME)-worker3 topology.kubernetes.io/zone=us-east-1b topology.kubernetes.io/rack=rack-2 --overwrite

deploy-demo:
	@echo "Deploying the Emojivoto demo application..."
	kubectl apply -f https://run.linkerd.io/emojivoto.yml
	@echo "Waiting 5 seconds for Kubernetes to create the Pod objects..."
	sleep 5
	@echo "Waiting for pods to be ready..."
	kubectl wait --for=condition=ready pod -l app.kubernetes.io/part-of=emojivoto -n emojivoto --timeout=90s

clean:
	@echo "Destroying the cluster..."
	kind delete cluster --name $(CLUSTER_NAME)

deep-clean: clean
	@echo "Pruning unused Docker images and volumes to free up disk space..."
	docker system prune -af --volumes