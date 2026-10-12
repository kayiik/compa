# The image the release publishes as ghcr.io/kayiik/compa, for anyone who
# hosts Compa: compa and the compa-kernel it runs, from the release build, as a
# non-root user, with its data in /data. curl is there for skills that call it;
# an install that needs more tools builds FROM this image.
#
# The release copies its linux binaries to bin/linux_<arch>/ before building:
#   docker buildx build -f docker/Dockerfile --platform linux/amd64,linux/arm64 .
#
# Compa listens on all addresses here, which it does only once a dashboard
# password is set: give the first one in a file, as a secret, with
# COMPA_DASHBOARD_PASSWORD_FILE, and the names it is reached by in
# COMPA_ALLOWED_HOSTS. See docs/install.md, Run in a container.
FROM debian:stable-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl tini \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --system --uid 10001 --user-group --home-dir /data --no-create-home compa \
	&& mkdir /data && chown compa:compa /data
ARG TARGETARCH
COPY --chmod=0755 bin/linux_${TARGETARCH}/compa bin/linux_${TARGETARCH}/compa-kernel /usr/local/bin/
COPY LICENSE NOTICE bin/linux_${TARGETARCH}/THIRD_PARTY_NOTICES /usr/share/doc/compa/
ENV COMPA_HOME=/data
VOLUME /data
USER compa
EXPOSE 18800
# tini reaps the gateway and the programs tools start, as PID 1 does not.
ENTRYPOINT ["tini", "--", "compa"]
CMD ["-console", "-no-browser", "-host", "0.0.0.0", "-port", "18800"]
