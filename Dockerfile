#FROM golang:1.13
#ENV GO111MODULE off
#WORKDIR /go/src/github.com/AliyunContainerService/ack-secret-manager
#COPY . .
#RUN make build

FROM alpine:3.11
WORKDIR /bin

#COPY --from=0 /go/src/github.com/AliyunContainerService/ack-secret-manager/build/bin/ack-secret-manager /bin/ack-secret-manager
ADD ./build/bin/ack-secret-manager /bin/ack-secret-manager

CMD ["./ack-secret-manager"]