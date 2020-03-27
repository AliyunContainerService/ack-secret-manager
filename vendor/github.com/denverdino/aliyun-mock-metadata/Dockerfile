FROM golang:1.11-alpine AS builder
MAINTAINER Li Yi <denverdino@gmail.com>
RUN apk add --no-cache git
WORKDIR /go/src/aliyun-mock-metadata
COPY *.go ./
COPY vendor ./vendor
#RUN go get -d -v ./...
#RUN go install -v ./...
RUN go build
RUN ls -la

# This image is like 13MB exported... :)
FROM alpine:3.9
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /go/src/aliyun-mock-metadata/aliyun-mock-metadata ./aliyun-mock-metadata
EXPOSE 45000
ENTRYPOINT ["./aliyun-mock-metadata", "--app-port", "45000"]
