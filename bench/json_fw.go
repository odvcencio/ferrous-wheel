package bench

import "encoding/json"

type Record struct {
	ID    int
	Name  string
	Value float64
}

//line json.fw:11
func JsonRoundtripFW(data []byte) ([]byte, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}
