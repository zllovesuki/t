FROM golang:1.17.1-buster as build
ARG BUILD_VERSION

WORKDIR /go/src/app
ADD . /go/src/app

RUN go get -d -v ./...
RUN CGO_ENABLED=0 go build -tags 'osusergo netgo' -ldflags "-X 'main.Version=$BUILD_VERSION' -s -w -extldflags -static" -a -o bin/t-server ./cmd/server

FROM gcr.io/distroless/static-debian10
COPY --from=build /go/src/app/bin/t-server /
CMD ["/t-server"]