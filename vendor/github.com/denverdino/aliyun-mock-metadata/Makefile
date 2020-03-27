VERSION_VAR := main.VERSION
REPO_VERSION := $(shell git describe --always --dirty --tags)
GOBUILD_VERSION_ARGS := -ldflags "-X $(VERSION_VAR)=$(REPO_VERSION)"
GIT_HASH := $(shell git rev-parse --short HEAD)

include .env

setup:
	go get -v
	go get -v -u github.com/githubnemo/CompileDaemon
	go get -v -u github.com/alecthomas/gometalinter
	gometalinter --install --update

build: *.go
	go fmt .
	go build -o bin/aliyun-mock-metadata $(GOBUILD_VERSION_ARGS) github.com/denverdino/aliyun-mock-metadata

test: check
	go test

junit-test: build
	go get github.com/jstemmer/go-junit-report
	go test -v | go-junit-report > test-report.xml

check: build
	gometalinter ./...

watch:
	CompileDaemon -color=true -build "make test"

commit-hook:
	cp dev/commit-hook.sh .git/hooks/pre-commit

cross:
	 CGO_ENABLED=0 GOOS=linux go build -ldflags "-s" -a -installsuffix cgo -o bin/aliyun-mock-metadata-linux .

docker: cross
	 docker build -t denverdino/aliyun-mock-metadata:$(GIT_HASH) .

version:
	@echo $(REPO_VERSION)

run:
	@ACCESS_KEY_ID=$(ACCESS_KEY_ID) ACCESS_KEY_SECRET=$(ACCESS_KEY_SECRET) \
		SECURITY_TOKEN=$(SECURITY_TOKEN) bin/aliyun-mock-metadata --zone-id=$(ZONE_ID) \
		--instance-id=$(INSTANCE_ID) --hostname=$(HOSTNAME) --role-name=$(ROLE_NAME) --role-arn=$(ROLE_ARN) \
		--app-port=$(APP_PORT)

run-macos:
	bin/server-macos

run-linux:
	bin/server-linux

run-docker:
	@docker run -it --rm -p 80:$(APP_PORT) -e ACCESS_KEY_ID=$(ACCESS_KEY_ID) \
		-e ACCESS_KEY_SECRET=$(ACCESS_KEY_SECRET) -e SECURITY_TOKEN=$(SECURITY_TOKEN) \
		denverdino/aliyun-mock-metadata:$(GIT_HASH) --zone-id=$(ZONE_ID) --instance-id=$(INSTANCE_ID) \
		--hostname=$(HOSTNAME) --role-name=$(ROLE_NAME) --role-arn=$(ROLE_ARN) --app-port=$(APP_PORT) \
		--vpc-id=$(VPC_ID) --private-ip=$(PRIVATE_IP)

clean:
	rm -f bin/aliyun-mock-metadata*
	docker rm $(shell docker ps -a -f 'status=exited' -q) || true
	docker rmi $(shell docker images -f 'dangling=true' -q) || true

release: docker
	docker push denverdino/aliyun-mock-metadata:$(GIT_HASH)
	docker tag denverdino/aliyun-mock-metadata:$(GIT_HASH) denverdino/aliyun-mock-metadata:latest
	docker push denverdino/aliyun-mock-metadata:latest