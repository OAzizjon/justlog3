// Build both Node module formats and their TypeScript declarations.
import { defineConfig } from 'tsup';

export default defineConfig({
    entry: ['src/index.ts'],
    format: ['esm', 'cjs'],
    target: 'node18',
    dts: true,
    clean: true,
    splitting: false,
});
