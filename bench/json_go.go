package bench

import "encoding/json"

// JsonRoundtripGo is the hand-written Go equivalent of JsonRoundtripFW.
func JsonRoundtripGo(data []byte) ([]byte, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}
