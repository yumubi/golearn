import antfu from '@antfu/eslint-config'

export default antfu({
  type: 'app',
  pnpm: true,
  antislop: true,
  ignores: [
    'dnsproxi-go/**',
    '**/exercises/**',
    '**/labs/**',
    'vendored/**',
    '**/*.css',
  ],
  rules: {
    'style/quotes': ['error', 'single'],
  },
}, {
  name: 'handbook-harness-js',
  files: ['.omp/handbook/*.js'],
  rules: {
    // The unused-imports rule misclassifies ordinary JS reads as type-only on ESLint 10.
    // Keep equivalent unused-variable checking with ESLint's core rule for these CLIs.
    'unused-imports/no-unused-vars': 'off',
    'no-unused-vars': ['error', { args: 'after-used', argsIgnorePattern: '^_', ignoreRestSiblings: true }],
  },
})
