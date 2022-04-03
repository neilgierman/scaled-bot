deploy: build
	echo 'Make sure you have `make create-cluster` first'
	docker build -t bot:latest .
	docker tag bot:latest k3d-quake-registry.localhost:$(shell docker inspect k3d-quake-registry | jq -r '.[0].NetworkSettings.Ports."5000/tcp"' | jq -r .[0].HostPort)/bot:latest
	docker push k3d-quake-registry.localhost:$(shell docker inspect k3d-quake-registry | jq -r '.[0].NetworkSettings.Ports."5000/tcp"' | jq -r .[0].HostPort)/bot:latest
	kubectl config use-context k3d-quake
	kubectl apply -f rbac.yml
	kubectl apply -f redis-service.yml
	kubectl apply -f redis-deployment.yml
	kubectl apply -f bot-deployment.yml
	kubectl apply -f bot-hpa.yml

destroy:
	-kubectl config use-context k3d-quake
	-kubectl delete -f bot-hpa.yml
	-kubectl delete -f bot-deployment.yml
	-kubectl delete -f redis-deployment.yml
	-kubectl delete -f redis-service.yml
	-kubectl delete -f rbac.yml

create-cluster:
	k3d cluster create quake --registry-create

delete-cluster:
	k3d cluster delete quake
	docker network rm k3d-quake

clean:
	rm -f cmd/bot

build:
	go mod tidy
	go mod vendor
	cd cmd && CGO_ENABLED=0 go build -ldflags="-s -w" -o bot && cd ..
