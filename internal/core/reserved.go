package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/lucawalz/horizon/internal/provider"
)

const reservedNameEntropyBytes = 8

func reservedSelector() map[string]string {
	return map[string]string{provider.PoolLabelKey: provider.ReservedPoolValue}
}

func listReserved(ctx context.Context, prov provider.Provider) ([]provider.Instance, error) {
	return prov.List(ctx, reservedSelector())
}

func scaleReservedTo(ctx context.Context, prov provider.Provider, want int) error {
	if want < 0 {
		return fmt.Errorf("desired reserved count must not be negative")
	}
	current, err := listReserved(ctx, prov)
	if err != nil {
		return err
	}
	for i := len(current); i < want; i++ {
		name, err := reservedInstanceName()
		if err != nil {
			return err
		}
		if _, err := prov.Create(ctx, provider.CreateRequest{Name: name, Labels: reservedSelector()}); err != nil {
			return err
		}
	}
	for i := len(current) - 1; i >= want; i-- {
		if err := prov.Delete(ctx, current[i].Name); err != nil {
			return err
		}
	}
	return nil
}

func reservedInstanceName() (string, error) {
	b := make([]byte, reservedNameEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate instance name: %w", err)
	}
	return provider.ReservedPoolValue + "-" + hex.EncodeToString(b), nil
}
