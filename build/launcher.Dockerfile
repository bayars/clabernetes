FROM golang:1.24-bookworm AS builder

ARG VERSION

WORKDIR /clabernetes

RUN mkdir build

COPY . .

RUN go mod download

RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build \
    -ldflags "-s -w -X github.com/srl-labs/clabernetes/constants.Version=${VERSION}" \
    -trimpath \
    -a \
    -o \
    build/manager \
    cmd/clabernetes/main.go

FROM --platform=linux/amd64 debian:bookworm-slim

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# RUNTIME_MODE controls which runtime variant to build:
#   "docker"    - (default) includes Docker CE for DinD mode
#   "containerd" - no Docker, uses host containerd socket directly; installs CNI plugins
ARG RUNTIME_MODE="docker"

ARG DOCKER_VERSION="5:28.*"
# note: there is/was a breakage for clab tools/vxlan tunnel between 0.52.0 and 0.56.x -- fixed in
# 0.57.5 of clab!
ARG CONTAINERLAB_VERSION="0.73.0+"
ARG NERDCTL_VERSION="2.1.4"
ARG CNI_PLUGINS_VERSION="1.6.2"

RUN apt-get update && \
    apt-get install -yq --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    gnupg \
    lsb-release \
    vim \
    iproute2 \
    tcpdump \
    procps \
    openssh-client \
    inetutils-ping \
    traceroute

RUN echo "deb [trusted=yes] https://apt.fury.io/netdevops/ /" | \
    tee -a /etc/apt/sources.list.d/netdevops.list

# Docker apt repo is needed for both modes (docker mode installs docker-ce; containerd mode
# may still want docker-ce-cli for debugging, but we skip it to keep the image small)
RUN curl -fsSL https://download.docker.com/linux/debian/gpg | \
    gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

RUN echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian \
    $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null

# Install containerlab + runtime-specific packages
RUN apt-get update && \
    if [ "$RUNTIME_MODE" = "containerd" ]; then \
        apt-get install -yq --no-install-recommends \
            containerlab=${CONTAINERLAB_VERSION} ; \
    else \
        apt-get install -yq --no-install-recommends \
            containerlab=${CONTAINERLAB_VERSION} \
            docker-ce=${DOCKER_VERSION} \
            docker-ce-cli=${DOCKER_VERSION} ; \
    fi && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* /var/cache/apt/archive/*.deb

RUN curl -L https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-${NERDCTL_VERSION}-linux-amd64.tar.gz | tar -xz -C /usr/bin/ && rm -f /usr/bin/containerd-rootless*.sh

# Install CNI plugins (needed for containerd runtime networking)
RUN if [ "$RUNTIME_MODE" = "containerd" ]; then \
        mkdir -p /opt/cni/bin && \
        curl -L https://github.com/containernetworking/plugins/releases/download/v${CNI_PLUGINS_VERSION}/cni-plugins-linux-amd64-v${CNI_PLUGINS_VERSION}.tgz | tar -xz -C /opt/cni/bin/ ; \
    fi

# Replace the apt-installed containerlab with our fork binary (which includes the containerd
# runtime) in containerd mode.
COPY build/containerlab_fork /tmp/containerlab_fork
RUN if [ "$RUNTIME_MODE" = "containerd" ]; then \
        mv /tmp/containerlab_fork /usr/bin/containerlab && \
        chmod +x /usr/bin/containerlab ; \
    else \
        rm -f /tmp/containerlab_fork ; \
    fi

# https://github.com/docker/cli/issues/4807
RUN if [ "$RUNTIME_MODE" = "docker" ] && [ -f /etc/init.d/docker ]; then \
        sed -i 's/ulimit -Hn/# ulimit -Hn/g' /etc/init.d/docker ; \
    fi

# copy a basic but nicer than standard bashrc for the user
COPY build/launcher/.bashrc /root/.bashrc

# copy default ssh keys to the launcher image
# to make use of password-less ssh access
COPY build/launcher/default_id_rsa /root/.ssh/id_rsa
COPY build/launcher/default_id_rsa.pub /root/.ssh/id_rsa.pub
RUN chmod 600 /root/.ssh/id_rsa

# copy custom ssh config to enable easy ssh access from launcher
COPY build/launcher/ssh_config /etc/ssh/ssh_config

# copy sshin command to simplify ssh access to the containers
COPY build/launcher/sshin /usr/local/bin/sshin

# copy shellin command to simplify shell access to the containers
COPY build/launcher/shellin /usr/local/bin/shellin

WORKDIR /clabernetes

RUN mkdir .node
# .image directory is only needed for docker mode (image export/import), but create it
# unconditionally to avoid breaking anything
RUN mkdir .image

COPY --from=builder /clabernetes/build/manager .
USER root

ENTRYPOINT ["/clabernetes/manager", "launch"]
