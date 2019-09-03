# NOTE: this Dockerfile is purely for local development! it is *not* used for
# the official 'concourse/concourse' image.

FROM concourse/dev

RUN apt-get update && apt-get install -y curl

RUN mkdir -p /opt/cni/bin
RUN curl -L https://github.com/containernetworking/plugins/releases/download/v0.8.2/cni-plugins-linux-amd64-v0.8.2.tgz | tar -zxf - -C /opt/cni/bin

RUN mkdir -p /opt/containerd/bin
RUN curl -L https://github.com/containerd/containerd/releases/download/v1.3.0-beta.2/containerd-1.3.0-beta.2.linux-amd64.tar.gz | tar -zxf - -C /opt/containerd
RUN curl -L https://github.com/opencontainers/runc/releases/download/v1.0.0-rc8/runc.amd64 -o /opt/containerd/bin/runc && chmod +x /opt/containerd/bin/runc
RUN apt-get update && apt-get -y install iptables
ENV PATH /opt/containerd/bin:$PATH

# download go modules separately so this doesn't re-run on every change
WORKDIR /src
COPY go.mod .
COPY go.sum .
RUN grep '^replace' go.mod || go mod download

# build Concourse without using 'packr' and set up a volume so the web assets
# live-update
COPY . .
RUN go build -gcflags=all="-N -l" -o /usr/local/concourse/bin/concourse \
      ./cmd/concourse
VOLUME /src

# generate keys (with 1024 bits just so they generate faster)
RUN mkdir -p /concourse-keys
RUN concourse generate-key -t rsa -b 1024 -f /concourse-keys/session_signing_key
RUN concourse generate-key -t ssh -b 1024 -f /concourse-keys/tsa_host_key
RUN concourse generate-key -t ssh -b 1024 -f /concourse-keys/worker_key
RUN cp /concourse-keys/worker_key.pub /concourse-keys/authorized_worker_keys
