# Changelog

## Unreleased

- Breaking: subscribe to `zone_update { id state { zone_room_temperature zone_room_humidity } }` in coordination with the gateway naming wave. Decode nested state; previous spellings have no aliases or fallback. Zone filtering, per-field last known values, units, freshness and reconnect behavior are preserved.
