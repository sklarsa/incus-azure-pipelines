# Incus-powered Azure Pipelines Agents

This project aims to provide a runtime for self-hosted Azure Pipelines Agents inside ephemeral [Incus](https://linuxcontainers.org/incus/) system containers.

The goals of this project are to:
1. Provide an OS-like testing environment. This includes services like systemd, and the ability to run a docker daemon.
2. Use independent environments for every job. Azure self-hosted agents do not clean themselves up by default, which can lead to problems like running out of disk space, or difficulty in creating reproduceable builds.


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
