# Incus-powered Azure Pipelines Agents

This project aims to provide a runtime for self-hosted Azure Pipelines Agents inside ephemeral [Incus](https://linuxcontainers.org/incus/) system containers.

The goals of this project are to:
1. Provide an OS-like testing environment for Azure Pipelines Agents. This includes access to services like systemd and the ability to easily run a docker daemon without resorting to any docker-in-docker wizardy.
2. Use an independent environment for every CI job. Azure self-hosted agents do not clean themselves up by default, which can lead to problems like running out of disk space on the host, or difficulty in creating reproduceable builds.

## Why go through all of this trouble?

It appears that Azure Devops limits the maximum amount of parallelism to 25 Azure-hosted agents. Even if you want to pay for more, the UI won't let you do it. At least on whatever plan my organization is using.

But there's **no limit!!** to the parallelism of self-hosted workers (that you can pay for, of course). To take advantage of this, my company started with a lambda that spun up an EC2 instance at the beginning of every Azure job, and destroyed the instance once the job was completed. This obviously added a lot more complexity to the pipeline infrastructure along with making it more brittle and opaque. It also started getting expensive.

So we rented a bunch of Hetzner auction servers and ran long-lived agents in Docker containers. Price-and-complexity-wise, this was a great win for us (as compared to the lambda-driven method). But we were still missing clean Docker-in-Docker builds, had to ensure that _every_ job cleaned itself up appropriately (by marking it as `workspace.clean = all` in the pipeline yaml), and still ended up with a mess of a test execution environments after running heterogeneous tests that installed various packages and utilities along the way.

Finally, I hatched a plan to use ephemeral "system containers" and the Azure agent's `./run.sh --once` option to solve some of these pain points. And this is what resulted from that work!

## How it works

### Container lifecycle
1. A new container is created using a pre-built base image as its source
2. The container boots
3. The orchestrator injects credentials onto the container's filesystem that are used to register the agent with Azure Devops
4. The orchestrator execs a pre-installed wrapper script that:
    - Reads the credentials into memory
    - Deletes the credentials file
    - Runs `./config.sh` to register the agent
    - Runs `./run.sh --once` to pick up a single CI job
    - Issues `sudo poweroff -f` after the job is complete
5. A CI job is picked up and completed
6. Since we are using ephemeral containers, on shutdown, the container will be reaped automatically by the Incus daemon
7. The orchestrator will be notified that it needs to replace the deleted agent one of two ways:
    - Identifying that the agent container has been deleted via a subscription to the Incus event stream
    - A regularly-scheduled reconcile job which ensures all required agent containers exist

## How to deploy it

### On the Azure Pipelines side

### Setup Incus

First, you need a deployed and running version of Incus. There are more knobs to tweak here than simply installing `docker`, so I leave this an an exercise to the user.

I do recommend using a storage backend that supports Copy on Write (COW), like btrfs or zfs. This will make your new Agent containers spin up almost instantly, instead of having to wait for the Incus daemon to unpack and write the contents of the base container image to the new container's image location.

I also recommend creating a separate Incus project for your pipeline runners to keep your workloads isolated from each other. But be careful! When doing this, you **need to create a profile for the new project** before using it, otherwise you'll have problems and won't be able to run any containers.

### Configure

Once you're finished with your Incus setup, it's time get cooking with this software.

First, you need to craft a config file. See the [configuration schema](https://sklarsa.github.io/incus-azure-pipelines/schema.json) for all available options.

```yaml
---
projectName: azure-pipelines
agentCount: 8
baseImage: ubuntu/24.04
maxCores: 8
maxRamInGb: 4
tmpfsSizeInGb: 12
provisionScripts:
- /tmp/script1.sh
- /tmp/script2.sh
azure:
  pat: myVerySecretPatHere
  url: https://dev.azure.com/<my-organization>
  pool: myAgentPool
```

### Create a base image

You first need to build the base image that your runners will use. We install some basic utilities and pre-provision the `agent` user, since the Azure Pipelines Agent should not be run as `root`.

You can also add your own custom provisioning by writing scripts and adding them to the `provisionScripts` list in the config. These scripts will be executed in order.

```bash
incus-azure-pipelines -provision -config $PATH_OF_CONFIG_FILE
```

### Run the orchestrator

Finally, start the orchestrator using some daemonizer (most likely systemd, let's be honest) and see your Agents come to life.

The command to run the orchestrator is

```bash
incus-azure-pipelines -run -config $PATH_OF_CONFIG_FILE`
```
