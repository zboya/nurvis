// Type declarations for Wails3-generated service.js.
// Generation command: cd cmd/nurvis-desktop && wails3 generate bindings -d frontend/bindings
// Wails regenerates and clears the target directory, so declarations are placed under src/types to avoid being overwritten.

declare module '*/cmd/nurvis-desktop/service.js' {
  /** Returns the Gateway listen address, e.g. ":18981" or "127.0.0.1:18981". */
  export function GetGatewayAddr(): Promise<string>
  /** Opens a system directory picker; returns the selected path, or empty string if cancelled. */
  export function SelectDirectory(): Promise<string>
  /** Opens a system file picker (multi-select); returns selected absolute file paths, or empty array if cancelled. */
  export function SelectFiles(): Promise<string[]>
}
