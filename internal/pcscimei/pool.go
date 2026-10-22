package pcscimei

import (
	"errors"
	"strings"
)

// DefaultPool contains the synthetic IMEIs available to USB SIM readers.
var DefaultPool = []string{
	"866069053659148",
	"866069052438940",
	"866069053083133",
	"866069053222707",
	"866069051202057",
	"866069054181407",
	"866069050999893",
	"866069051190162",
	"866069053452247",
	"866069053461198",
	"863285069344664",
	"863212061358591",
	"866069051772448",
	"866069053101505",
	"866069052517214",
	"863212061734262",
	"863212060607550",
	"866069052477120",
	"863212060571335",
	"863285068498933",
}

var ErrInvalidIMEI = errors.New("pcscimei: IMEI must contain exactly 15 digits")

func Normalize(value string) string {
	return strings.TrimSpace(value)
}

func Validate(value string) error {
	value = Normalize(value)
	if len(value) != 15 {
		return ErrInvalidIMEI
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return ErrInvalidIMEI
		}
	}
	return nil
}
