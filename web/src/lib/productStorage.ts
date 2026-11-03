// Niuma keeps an independent browser-storage namespace and never imports
// values from another product's installation.
export function readProductStorage(key: string): string | null {
  return window.localStorage.getItem(key);
}
