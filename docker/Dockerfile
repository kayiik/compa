# The image the release publishes as ghcr.io/kayiik/compa, for anyone who
# hosts Compa: compa and the compa-kernel it runs, from the release build, as a
# non-root user, with its data in /data. curl and sqlite3 are there for skills
# that call them.
#
# The release copies its linux binaries to bin/linux_<arch>/ before building:
#   docker buildx build -f docker/Dockerfile --platform linux/amd64,linux/arm64 .
#
# Compa listens on all addresses here, which it does only once a dashboard
# password is set, so set one first:
#   docker run --rm -i -v compa-data:/data ghcr.io/kayiik/compa -password -
#   docker run -d -p 18800:18800 -v compa-data:/data ghcr.io/kayiik/compa
FROM debian:stable-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl sqlite3 \
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
ENTRYPOINT ["compa"]
CMD ["-console", "-no-browser", "-host", "0.0.0.0", "-port", "18800"]
