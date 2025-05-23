// Package webserver provides a framework to impelemnt etl transformation webserver in golang.
/*
 * Copyright (c) 2025, NVIDIA CORPORATION. All rights reserved.
 */
package webserver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/NVIDIA/aistore/cmn/cos"
)

const GetContentType = "binary/octet-stream"

func setResponseHeaders(header http.Header, size int64) {
	header.Set(cos.HdrContentLength, strconv.FormatInt(size, 10))
	header.Set(cos.HdrContentType, GetContentType)
}

// Returns an error with message if status code was > 200
func wrapHTTPError(resp *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return resp, err
	}

	if resp.StatusCode > http.StatusNoContent {
		if resp.Body == nil {
			return resp, errors.New(resp.Status)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp, err
		}
		return resp, fmt.Errorf("%s %s", resp.Status, string(b))
	}

	return resp, nil
}
