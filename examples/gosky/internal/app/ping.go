package app

type PingResponse struct {
	Message string `json:"message"`
}

func Ping() PingResponse {
	return PingResponse{Message: "pong"}
}
