export type RendererPlatform = 'darwin' | 'win32' | 'other';

export function detectRendererPlatform(userAgent: string): RendererPlatform {
  if (/\bWindows\b/i.test(userAgent)) return 'win32';
  if (/\bMacintosh\b|\bMac OS X\b/i.test(userAgent)) return 'darwin';
  return 'other';
}
