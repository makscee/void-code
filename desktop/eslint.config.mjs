import eslint from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    ignores: ['dist/**', 'release/**', 'resources/staged/**', 'runtime/pi/node_modules/**', 'node_modules/**'],
  },
  {
    files: ['**/*.ts'],
    rules: {
      '@typescript-eslint/consistent-type-imports': 'error',
      '@typescript-eslint/no-explicit-any': 'error',
    },
  },
  {
    files: ['scripts/**/*.{mjs,cjs}'],
    languageOptions: { globals: { process: 'readonly', Buffer: 'readonly', console: 'readonly', setTimeout: 'readonly', require: 'readonly', exports: 'readonly', __filename: 'readonly', __dirname: 'readonly', fetch: 'readonly', AbortSignal: 'readonly' } },
  },
  {
    files: ['scripts/**/*.cjs'],
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
  {
    // The two plain-JS files of the main process. They are CommonJS deliberately: a worker thread
    // loads them by relative path in both src/main and dist/main (see private-runtime.js), and the
    // compiled main process around them is CommonJS too.
    files: ['src/main/*.js'],
    languageOptions: { globals: { process: 'readonly', Buffer: 'readonly', require: 'readonly', module: 'writable', __filename: 'readonly', __dirname: 'readonly' } },
    rules: { '@typescript-eslint/no-require-imports': 'off' },
  },
);
