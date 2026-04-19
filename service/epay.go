package service

import (
	"errors"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func GetCallbackAddress() string {
	if customAddress := normalizeCallbackAddress(operation_setting.CustomCallbackAddress); customAddress != "" {
		return customAddress
	}
	return normalizeCallbackAddress(system_setting.ServerAddress)
}

func normalizeCallbackAddress(address string) string {
	address = strings.TrimSpace(address)
	switch strings.ToLower(address) {
	case "", "<nil>", "nil", "null", "undefined":
		return ""
	default:
		return strings.TrimRight(address, "/")
	}
}

func BuildCallbackURL(callbackPath string) (*url.URL, error) {
	baseAddress := GetCallbackAddress()
	if baseAddress == "" {
		return nil, errors.New("callback address is empty")
	}

	rawURL := strings.TrimRight(baseAddress, "/") + "/" + strings.TrimLeft(callbackPath, "/")
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, errors.New("callback address must be an absolute URL")
	}
	return parsedURL, nil
}
