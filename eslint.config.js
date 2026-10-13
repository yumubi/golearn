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
})
