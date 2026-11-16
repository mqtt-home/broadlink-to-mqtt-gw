# Build from the repository root:  docker build -t broadlink-to-mqtt-gw .

FROM node:22-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
# vite writes to ../app/web/dist
RUN mkdir -p /src/app/web/dist && npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src/app
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
COPY --from=ui /src/app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /bl2mqtt . \
 && mkdir /data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /bl2mqtt /bl2mqtt
# Writable volume for commands managed in the web UI (see "data-file").
COPY --from=build --chown=65532:65532 /data /data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/bl2mqtt"]
CMD ["/var/lib/bl2mqtt/config.json"]
