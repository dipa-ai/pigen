FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /pigen ./cmd/pigen

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pigen /pigen
EXPOSE 31415/tcp 31415/udp 8080/tcp
USER nonroot:nonroot
ENTRYPOINT ["/pigen"]
