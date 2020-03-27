# aliyun-mock-metadata

This project is the fork from [github.com/jtblin/aws-mock-metadata](https://github.com/jtblin/aws-mock-metadata) for Alibaba Cloud

The [ECS instance metadata service](https://www.alibabacloud.com/help/zh/doc-detail/49122.htm)
runs on each ECS instance and provide an api to retrieve information about the running instance as well as 
getting credentials based on the RAM role. 


## Docker quick start

	docker run -it --rm -p 80:8080 -e ACCESS_KEY_ID=$(ACCESS_KEY_ID) \
    		-e ACCESS_KEY_SECRET=$(ACCESS_KEY_SECRET) denverdino/aliyun-mock-metadata \
    		--zone-id=<az> --instance-id=<id> --hostname=<name> --role-name=<role> --role-arn=<arn>
    		--vpc-id=<vpc-id> --private-ip=<ip>

In your other docker image, install iptables and have a startup script that point 100.100.100.200 to the docker host
before starting your program:

	iptables -t nat -A OUTPUT -d 100.100.100.200 -j DNAT --to-destination ${HOST}

Or if you don't want to modify your docker image, on your docker host (e.g. the one created with docker-machine):

	iptables -t nat -A PREROUTING -d 100.100.100.200 -j DNAT --to-destination ${HOST}

## Development

### Configuration

Set the following environment variables or create a .env file with the following information:

* `ACCESS_KEY_ID`: Access key ID
* `ACCESS_KEY_SECRET`:  Access key secret

Command line arguments:

* `APP_PORT`: port to run the container on (default 8080)
* `ZONE_ID`: ECS availability zone e.g. cn-shanghai-e (optional)
* `SECURITY_TOKEN`: Session token (optional)
* `HOSTNAME`: ECS hostname (optional)
* `INSTANCE_ID`: ECS instance id (optional)
* `PRIVATE_IP`: ECS private ip address (optional)
* `ROLE_ARN`: arn for the role to assume to generate temporary credentials (optional)
* `ROLE_NAME`: ECS role name assigned to the instance (optional)
* `VPC_ID`: vpc id (optional)

**Note**: you will need to have `sts:AssumeRole` for the role that you want to use to generate temporary credentials.
The role also needs to have a trust relationship with the account that you use to assume the role.

### Run

Run it. This will run the bare server on localhost.

    make build run

Run it on 100.100.100.200 on Mac OSX or linux.

    make build run-macos
    make build run-linux

Run in docker

	make docker run-docker
