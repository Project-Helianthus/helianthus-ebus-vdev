package datasource

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestGraphQLZoneSource_NoData(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData", err)
	}

	_, err = src.Humidity()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Humidity() err = %v; want ErrNoData", err)
	}
}

func TestGraphQLZoneSource_Update(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	src.Update(21.5, 45.0)

	temp, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v", err)
	}
	if temp != 21.5 {
		t.Fatalf("Temperature() = %f; want 21.5", temp)
	}

	humid, err := src.Humidity()
	if err != nil {
		t.Fatalf("Humidity() err = %v", err)
	}
	if humid != 45.0 {
		t.Fatalf("Humidity() = %f; want 45.0", humid)
	}
}

func TestGraphQLZoneSource_StaleData(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
		StaleTTL:   1 * time.Millisecond,
	})

	src.Update(21.5, 45.0)
	time.Sleep(5 * time.Millisecond)

	_, err := src.Temperature()
	if !errors.Is(err, ErrStaleData) {
		t.Fatalf("Temperature() err = %v; want ErrStaleData", err)
	}

	_, err = src.Humidity()
	if !errors.Is(err, ErrStaleData) {
		t.Fatalf("Humidity() err = %v; want ErrStaleData", err)
	}
}

func TestGraphQLZoneSource_DefaultStaleTTL(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	if src.config.StaleTTL != defaultStaleTTL {
		t.Fatalf("StaleTTL = %v; want %v", src.config.StaleTTL, defaultStaleTTL)
	}
}

func TestGraphQLZoneSource_ProcessMessage(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"data","id":"1","payload":{"data":{"zoneUpdate":{"id":"zone-2","currentTempC":22.5,"currentHumidityPct":55.0}}}}`)
	src.processMessage(msg)

	temp, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v", err)
	}
	if temp != 22.5 {
		t.Fatalf("Temperature() = %f; want 22.5", temp)
	}

	humid, err := src.Humidity()
	if err != nil {
		t.Fatalf("Humidity() err = %v", err)
	}
	if humid != 55.0 {
		t.Fatalf("Humidity() = %f; want 55.0", humid)
	}
}

func TestGraphQLZoneSource_ProcessMessage_WrongZone(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"data","id":"1","payload":{"data":{"zoneUpdate":{"id":"zone-1","currentTempC":22.5,"currentHumidityPct":55.0}}}}`)
	src.processMessage(msg)

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData (wrong zone filtered)", err)
	}
}

func TestGraphQLZoneSource_ProcessMessage_NonDataType(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	msg := []byte(`{"type":"connection_ack"}`)
	src.processMessage(msg)

	_, err := src.Temperature()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Temperature() err = %v; want ErrNoData", err)
	}
}

func TestGraphQLZoneSource_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	src := NewGraphQLZoneSource(GraphQLZoneConfig{
		GatewayURL: "ws://localhost:8080/graphql",
		ZoneID:     "zone-2",
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			src.Update(float64(i), float64(i))
		}(i)
		go func() {
			defer wg.Done()
			src.Temperature()
			src.Humidity()
		}()
	}
	wg.Wait()

	// Just verify no panic/race.
	_, err := src.Temperature()
	if err != nil {
		t.Fatalf("Temperature() err = %v after concurrent access", err)
	}
}
