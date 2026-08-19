export function beginStartupUpdateCheck(check: () => Promise<unknown>): void {
  void check();
}
