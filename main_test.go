package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/stretchr/testify/require"
)

func TestAccountCreation(t *testing.T) {
	ctx := context.Background()
	httpClient := http.Client{}

	for i := 0; i < 20; i++ {
		body := CreateAccountRequest{
			Email:     gofakeit.Email(),
			Password:  gofakeit.Password(true, true, true, true, false, 12),
			FirstName: gofakeit.FirstName(),
			LastName:  gofakeit.LastName(),
		}
		payload, err := json.Marshal(body)
		require.NoError(t, err)

		t.Log(string(payload))

		req, err := http.NewRequestWithContext(
			ctx,
			"POST",
			"http://localhost:8080/accounts",
			bytes.NewReader(payload),
		)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}
}
