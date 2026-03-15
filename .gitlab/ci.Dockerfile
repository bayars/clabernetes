FROM golang:1.24

ARG DEVSPACE_VERSION=v6.3.15
ARG GCI_VERSION=v0.13.7
ARG GOFUMPT_VERSION=v0.9.1
ARG GOLANGCI_LINT_VERSION=v2.4.0
ARG GOLINES_VERSION=v0.12.2
ARG GOTESTSUM_VERSION=v1.12.2
ARG HELM_VERSION=v3.18.2
ARG GORELEASER_VERSION=v2.10.2
ARG KIND_VERSION=v0.22.0
ARG KUBECTL_VERSION=v1.32.0
ARG YQ_VERSION=v4.44.6

# go tools
RUN go install mvdan.cc/gofumpt@${GOFUMPT_VERSION} && \
    go install github.com/daixiang0/gci@${GCI_VERSION} && \
    go install github.com/segmentio/golines@${GOLINES_VERSION} && \
    go install gotest.tools/gotestsum@${GOTESTSUM_VERSION}

# golangci-lint
RUN curl -sSfL \
      https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
      sh -s -- -b /usr/local/bin ${GOLANGCI_LINT_VERSION}

# helm
RUN curl -L -o /tmp/helm.tar.gz \
      "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" && \
    tar -zxf /tmp/helm.tar.gz -C /tmp && \
    mv /tmp/linux-amd64/helm /usr/local/bin/helm && \
    rm -rf /tmp/helm.tar.gz /tmp/linux-amd64

# devspace
RUN curl -L -o /usr/local/bin/devspace \
      "https://github.com/loft-sh/devspace/releases/download/${DEVSPACE_VERSION}/devspace-linux-amd64" && \
    chmod +x /usr/local/bin/devspace

# goreleaser
RUN curl -L -o /tmp/goreleaser.tar.gz \
      "https://github.com/goreleaser/goreleaser/releases/download/${GORELEASER_VERSION}/goreleaser_Linux_x86_64.tar.gz" && \
    tar -zxf /tmp/goreleaser.tar.gz -C /tmp && \
    mv /tmp/goreleaser /usr/local/bin/goreleaser && \
    rm -f /tmp/goreleaser.tar.gz

# kubectl
RUN curl -L -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" && \
    chmod +x /usr/local/bin/kubectl

# kind
RUN curl -L -o /usr/local/bin/kind \
      "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-amd64" && \
    chmod +x /usr/local/bin/kind

# yq
RUN curl -L -o /usr/local/bin/yq \
      "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" && \
    chmod +x /usr/local/bin/yq

# docker cli (for dind builds)
RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends docker.io && \
    rm -rf /var/lib/apt/lists/*
