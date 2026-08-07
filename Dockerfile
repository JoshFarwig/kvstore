FROM golang:1.26-alpine AS build
WORKDIR /src 
COPY go.mod go.sum ./ 
RUN go mod download
COPY . . 
RUN CGO_ENABLED=0 go build -o /kvstore .

FROM alpine:3 
COPY --from=build /kvstore /kvstore
EXPOSE 8080
ENTRYPOINT [ "/kvstore" ]
