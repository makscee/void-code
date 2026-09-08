type DropTarget = {
  addEventListener(type: 'dragover' | 'drop', listener: (event: DragEvent) => void): void;
};

type FileDropDependencies = {
  target: DropTarget;
  getPathForFile(file: File): string;
  getCurrentTerminal(): { paste(value: string): void } | undefined;
};

function hasFiles(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes('Files');
}

export function installFileDropHandlers({ target, getPathForFile, getCurrentTerminal }: FileDropDependencies): void {
  target.addEventListener('dragover', (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  });
  target.addEventListener('drop', (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    const terminal = getCurrentTerminal();
    if (!terminal || !event.dataTransfer) return;
    const paths = Array.from(event.dataTransfer.files, (file) => getPathForFile(file)).filter((path) => path !== '');
    if (paths.length > 0) terminal.paste(paths.join('\n'));
  });
}
