FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY server ./server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./server

FROM scratch
COPY --from=build /gateway /gateway
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/gateway"]
