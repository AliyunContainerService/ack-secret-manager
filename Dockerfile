FROM --platform=$BUILDPLATFORM golang:1.23.1 as builder
ARG TARGETOS TARGETARCH
ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE off
WORKDIR /go/src/github.com/AliyunContainerService/ack-secret-manager
COPY . .
RUN make build

FROM --platform=$TARGETPLATFORM alpine:3.11.6
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories
WORKDIR /bin

RUN apk update && apk upgrade
RUN apk add --no-cache ca-certificates && \
    update-ca-certificates

COPY --from=builder /go/src/github.com/AliyunContainerService/ack-secret-manager/build/bin/ack-secret-manager /bin/ack-secret-manager
#ADD ./build/bin/ack-secret-manager /bin/ack-secret-manager

CMD ["./ack-secret-manager"]