FROM toydium/golang-protoc:1.13.5

WORKDIR /polytank
ENV GO111MODULE=on
COPY . .
RUN go mod download
EXPOSE 33333

CMD ["tail", "-f", "/dev/null"]