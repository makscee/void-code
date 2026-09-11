import { afterEach, expect, it, vi } from 'vitest';
import { deferred, dispatch, renderer, settle } from './fixtures/desktop-keyboard-renderer';

let current: Awaited<ReturnType<typeof renderer>> | undefined;
afterEach(() => { current?.dispose(); current = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('K3/K4: CtrlTab during rename is not queued behind an unresolved B start after Escape', async () => {
  const s = current = await renderer();
  const tabs = () => s.node('#tabs');
  const titles = () => tabs().children.map(tab => tab.children[0].textContent);
  const selected = () => tabs().children.filter(tab => tab.className.split(' ').includes('selected')).map(tab => tab.children[0].getAttribute('aria-label'));
  const starts = ['a', 'b'].map(sessionId => [{ sessionId, cwd: '/synthetic-no-files', mode: 'resume' }]);
  expect(titles()).toEqual(['A', 'B', 'C']);
  expect(selected()).toEqual(['Rename A']);
  expect(s.start.mock.calls).toEqual(starts.slice(0, 1));
  const launch = deferred<{ showSharedFilesWarning: boolean }>();
  let resolved = false;
  s.start.mockImplementationOnce(async () => { await launch.promise; resolved = true; return { showSharedFilesWarning: false }; });

  s.key(); s.key({}, 'keyup'); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(selected()).toEqual(['Rename B']);
  expect(s.start.mock.calls).toEqual(starts);
  expect(resolved).toBe(false);
  tabs().children[1].children[0].click(); s.frame();
  const input = tabs().children[1].children[0];
  expect(input.tagName).toBe('INPUT');
  expect(input.getAttribute('aria-label')).toBe('Rename B');
  expect(s.doc.contains(input)).toBe(true);
  expect(document.activeElement).toBe(input);
  input.value = '  synthetic unfinished B  '; dispatch(input, 'input', {});

  s.key({}, 'keydown', input); s.key({}, 'keyup', input); await settle();
  expect(tabs().children[1].children[0]).toBe(input);
  expect(s.doc.contains(input)).toBe(true);
  expect(document.activeElement).toBe(input);
  expect(input.value).toBe('  synthetic unfinished B  ');
  expect(selected()).toEqual(['Rename B']);
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(s.start.mock.calls).toEqual(starts);
  expect(s.rename).not.toHaveBeenCalled();
  expect(resolved).toBe(false);

  dispatch(tabs().children[1].children[0], 'keydown', { key: 'Escape', code: 'Escape' });
  await settle();
  expect(tabs().children[1].children[0].tagName).toBe('BUTTON');
  expect(titles()).toEqual(['A', 'B', 'C']);
  expect(selected()).toEqual(['Rename B']);
  expect(s.doc.contains(input)).toBe(false);
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(s.start.mock.calls).toEqual(starts);
  expect(s.rename).not.toHaveBeenCalled();
  expect(resolved).toBe(false);

  launch.resolve({ showSharedFilesWarning: false }); await settle();
  expect(resolved).toBe(true);
  expect(selected()).toEqual(['Rename B']);
  expect(titles()).toEqual(['A', 'B', 'C']);
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(s.start.mock.calls).toEqual(starts);
  expect(s.rename).not.toHaveBeenCalled();
  expect(s.resume).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled();
});
