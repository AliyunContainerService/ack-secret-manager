FROM golang:1.23.1 as builder
ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE off
WORKDIR /go/src/github.com/AliyunContainerService/ack-secret-manager
COPY . .
RUN make build

FROM registry-cn-hangzhou.ack.aliyuncs.com/dev/alpine:3.20-base
WORKDIR /bin


COPY --from=builder /go/src/github.com/AliyunContainerService/ack-secret-manager/build/bin/ack-secret-manager /bin/ack-secret-manager
#ADD ./build/bin/ack-secret-manager /bin/ack-secret-manager

CMD ["./ack-secret-manager"]