syntax = v1

service {{.Name}} {
	@handler RemotePing
	get /remote returns (string)
}
