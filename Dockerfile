FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/codedeploy-agent ./cmd/codedeploy-agent

FROM gcr.io/distroless/static-debian12
COPY --from=build /bin/codedeploy-agent /codedeploy-agent
EXPOSE 8080
ENTRYPOINT ["/codedeploy-agent"]
