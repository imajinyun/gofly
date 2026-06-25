namespace go github.com/imajinyun/gofly/examples/rpc-idl-matrix/contracts

struct HelloRequest {
  1: required string name
}

struct HelloResponse {
  1: string message
}

struct ChatMessage {
  1: string from
  2: string text
}

service MatrixGreeter {
  HelloResponse SayHello(1: HelloRequest req)
  HelloResponse CollectHello(1: list<HelloRequest> reqs)
}
