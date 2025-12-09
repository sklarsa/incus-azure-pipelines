# Incus-powered Azure Pipelines Agents

This project aims to provide a runtime for self-hosted Azure Pipelines Agents inside ephemeral [Incus](https://linuxcontainers.org/incus/) system containers.

The goals of this project are to:
1. Provide an OS-like testing environment. This includes services like systemd, and the ability to run a docker daemon.
2. Use independent environments for every job. Azure self-hosted agents do not clean themselves up by default, which can lead to problems like running out of disk space, or difficulty in creating reproduceable builds.

## Why go through all of this trouble?

It appears that Azure Devops limits the maximum amount of parallelism to 25 Azure-hosted agents. Even if you want to pay for more, the UI won't let you do it. At least on whatever plan my organization is using.

But there's **no limit!!** to the parallelism of self-hosted workers. We started with an AWS lambda that spun up an EC2 instance for each job, but this added a lot more complexity to the infrastructure and the entire process was very opaque. It also started getting expensive.

We then started running Azure Pipelines Agents in Docker containers running on Hetzner auction servers. Price-and-complexity-wise, this was a great win! But we were still missing things like clean Docker-in-Docker builds, had to ensure that _every_ job cleaned itself up appropriately, and still ended up with a mess of a test execution environment.

Finally, I hatched a plan to use ephemeral "system containers" and the `./run.sh --once` option to solve some of these pain points. And this is what resulted!

## How it works

### Container lifecycle
1. A new container is created by `copy`ing a base container as its source
2. The container boots
3. The orchestrator injects credentials used to register the agent onto the container's filesystem
4. The orchestrator execs a wrapper script that:
    - reads the credentials into memory
    - deletes the credentials file
    - runs `./config.sh` to register the agent
    - runs `./run.sh --once` to pick up a single CI job
    - issues `sudo poweroff -f` after the job is complete
5. Since we are using ephemeral containers, on shutdown, the container will be reaped automatically by the Incus daemon
6. The orchestrator will be notified that it needs to replace the deleted agent one of two ways:
    - identifying that the agent container has been deleted via a subscription to the Incus event stream
    - a regularly-scheduled reconcile job which ensures all required agent containers exist

## How to deploy it

### On the Azure Pipelines side

### Setup Incus

First, you need a deployed and running version of Incus. There are more knobs to tweak here than simply installing `docker`, so I leave this an an exercise for the user.

I do recommend using a storage backend that supports Copy on Write (COW), like btrfs or zfs. These will make your new Agent containers spin up almost instantly, instead of having to wait for the Incus daemon to unpack and write the contents of the base container.

I also recommend creating a separate Incus project for your pipeline runners to keep everything isolated. But be careful! When doing this, you **need to create a profile for the new project** before using it, otherwise you'll have problems.

### Configure and run

Once you're finished with your Incus setup, it's time to run the orchestrator. 

First, you need to craft a config file


```yaml
---
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
maxCores: 8
maxRamInGb: 4
tmpfsSizeInGb: 12
provisionScripts:
- /tmp/script
azure:
  pat: myVerySecretPatHere
  url: https://dev.azure.com/<my-organization>
  pool: myAgentPool
```

Then, start the orchestrator using some daemonizer (most likely systemd, let's be honest) and see your Agents come to life.

The command to run the orchestrator is `incus-azure-pipelines -run -config $PATH_OF_CONFIG_FILE`
