package loganalytics

import (
	"fmt"
	"net/url"
	"strings"
)

func ParamsGetList(params url.Values, name string) (list []string, err error) {
	for _, v := range params[name] {
		list = append(list, strings.Split(v, ",")...)
	}
	return
}

func ParamsGetListRequired(params url.Values, name string) (list []string, err error) {
	list, err = ParamsGetList(params, name)

	if len(list) == 0 {
		err = fmt.Errorf("parameter \"%v\" is missing", name)
		return
	}

	return
}

// canonicalizeAzureLocation tries to get canonicalized azure location names
func canonicalizeAzureLocation(val string) string {
	val = strings.ToLower(val)
	// transform "west europe" into "westeurope"
	val = strings.ReplaceAll(val, " ", "")
	return val
}
