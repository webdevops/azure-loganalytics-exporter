FROM golang:1.16 as build

WORKDIR /go/src/github.com/webdevops/azure-loganalytics-exporter

# Get deps (cached)
COPY ./go.mod /go/src/github.com/webdevops/azure-loganalytics-exporter
COPY ./go.sum /go/src/github.com/webdevops/azure-loganalytics-exporter
RUN go mod download

# Compile
COPY ./ /go/src/github.com/webdevops/azure-loganalytics-exporter
RUN make test
RUN make lint
RUN make build
RUN ./azure-loganalytics-exporter --help

#############################################
# FINAL IMAGE
#############################################
FROM gcr.io/distroless/static
ENV LOG_JSON=1
COPY --from=build /go/src/github.com/webdevops/azure-loganalytics-exporter/azure-loganalytics-exporter /
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/azure-loganalytics-exporter"]
