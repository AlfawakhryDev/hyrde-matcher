.PHONY: test race bench lint up seed e2e load kind-up kind-down down

test:            ## unit tests
	go test ./...

race:            ## unit tests with the race detector
	go test -race ./...

bench:           ## ranking benchmark against 10,000 engineers
	go test ./internal/match -run x -bench Rank -benchmem

lint:
	gofmt -l . | (! grep .) && go vet ./...

up:              ## Kafka, DynamoDB, matcher and statsd in Docker
	docker compose up -d --build --wait

seed:            ## tables + 2,000 demo engineers
	AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local go run ./cmd/seed -engineers 2000

e2e:             ## end-to-end test against the running stack
	AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local go test -tags integration -count=1 ./e2e

load:            ## latency percentiles under load
	go run ./cmd/loadgen -n 20000 -c 32

kind-up:         ## the same services on a local Kubernetes cluster
	kind create cluster --name hyrde --wait 120s
	docker network connect go-api_default hyrde-control-plane
	docker build -t hyrde-matcher:dev --build-arg SERVICE=matcher .
	docker build -t hyrde-statsd:dev --build-arg SERVICE=statsd .
	kind load docker-image hyrde-matcher:dev --name hyrde
	kind load docker-image hyrde-statsd:dev --name hyrde
	kubectl apply -k deploy/k8s/local
	kubectl -n hyrde rollout status deploy/matcher --timeout=180s

kind-down:
	kind delete cluster --name hyrde

down:
	docker compose down
