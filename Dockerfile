FROM golang:1.16.12 as builder
ENV GO111MODULE off
WORKDIR /go/src/github.com/AliyunContainerService/ack-secret-manager
COPY . .
RUN make build

FROM alpine:3.11.6
WORKDIR /bin

RUN apk update && apk upgrade
RUN apk add --no-cache ca-certificates && \
    update-ca-certificates

COPY --from=builder /go/src/github.com/AliyunContainerService/ack-secret-manager/build/bin/ack-secret-manager /bin/ack-secret-manager
#ADD ./build/bin/ack-secret-manager /bin/ack-secret-manager

CMD ["./ack-secret-manager"]
