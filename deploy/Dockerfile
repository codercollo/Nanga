# One Dockerfile for every deployable: docker build --build-arg SERVICE=ledger .
FROM golang:1.22 AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN test -n "$SERVICE" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
