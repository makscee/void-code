type DropTarget = {
  addEventListener(type: 'dragover' | 'drop', listener: (event: DragEvent) => void): void;
};

type FileDropDependencies = {
  target: DropTarget;
  getPathForFile(file: File): string;
  getCurrentInput(): ((data: string) => void) | undefined;
};

function hasFiles(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes('Files');
}

function containsTerminalControl(path: string): boolean {
  for (let index = 0; index < path.length; index += 1) {
    const codeUnit = path.charCodeAt(index);
    if (codeUnit <= 0x1f || (codeUnit >= 0x7f && codeUnit <= 0x9f)) return true;
  }
  return false;
}

export function installFileDropHandlers({ target, getPathForFile, getCurrentInput }: FileDropDependencies): void {
  target.addEventListener('dragover', (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  });
  target.addEventListener('drop', (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    const input = getCurrentInput();
    if (!input || !event.dataTransfer) return;
    const paths = Array.from(event.dataTransfer.files, (file) => getPathForFile(file))
      .filter((path) => path !== '' && !containsTerminalControl(path));
    if (paths.length > 0) input(`\x1b[200~${paths.join('\n')}\x1b[201~`);
  });
}
