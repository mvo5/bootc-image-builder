FROM registry.fedoraproject.org/fedora:39 AS builder
RUN dnf install -y git-core golang gpgme-devel libassuan-devel && mkdir -p /build/bib
COPY bib/go.mod bib/go.sum /build/bib
RUN cd /build/bib && go mod download
COPY build.sh /build
COPY bib /build/bib
WORKDIR /build
RUN ./build.sh

FROM registry.fedoraproject.org/fedora:39
# Install newer osbuild to fix the loop bug, see
# - https://github.com/osbuild/bootc-image-builder/issues/7
# - https://github.com/osbuild/bootc-image-builder/issues/9
# - https://github.com/osbuild/osbuild/pull/1468
COPY ./group_osbuild-osbuild-fedora-39.repo /etc/yum.repos.d/

# XXX: remove once qemu is fixed in fedora,
# see https://github.com/osbuild/bootc-image-builder/issues/207
COPY ./michaelvogt-qemu-fix-bib-207-fedora-39.repo /etc/yum.repos.d/
RUN if [ "$(uname -m)" = "x86_64" ]; then \
       dnf install -y qemu-user-static-aarch64; \
    elif [ "$(uname -m)" = "arm64" ]; then \
       dnf install -y qemu-user-static-x86; \
    fi
# -----------------------------

COPY ./package-requires.txt .
RUN grep -vE '^#' package-requires.txt | xargs dnf install -y && rm -f package-requires.txt && dnf clean all
COPY --from=builder /build/bin/bootc-image-builder /usr/bin/bootc-image-builder
COPY entrypoint.sh /

ENTRYPOINT ["/entrypoint.sh"]
VOLUME /output
WORKDIR /output
VOLUME /store
VOLUME /rpmmd

