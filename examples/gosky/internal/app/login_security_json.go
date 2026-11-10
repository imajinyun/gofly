package app

import "encoding/json"

func jsonUnmarshalProtect(raw string, cfg *Level3ProtectConfig) error {
	return json.Unmarshal([]byte(raw), cfg)
}

func jsonMarshalProtect(cfg Level3ProtectConfig) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
