import { describe, expect, it } from 'vitest'

import { generateAnytlsURL, generateHysteria2URL, generateURL } from '../src/generator'

describe.each([
  { protocol: 'hysteria2', generate: generateHysteria2URL },
  { protocol: 'anytls', generate: generateAnytlsURL },
])('$protocol node names', ({ protocol, generate }) => {
  it.each(['my-node', 'node / #1'])('preserves the encoded fragment for %s', (hash) => {
    const params = { protocol, auth: 'secret', host: 'example.com', port: 443, params: {}, hash }

    expect(new URL(generate(params)).hash).toBe(`#${encodeURIComponent(hash)}`)
  })
})

it('preserves node link parameters and escaped names', () => {
  const link = generateURL({
    protocol: 'vless',
    username: 'synthetic-id',
    host: 'example.invalid',
    port: 443,
    params: { security: 'reality', sni: 'edge.example.invalid', path: '/ws?x=1', empty: '' },
    hash: 'node / #1',
  })
  const parsed = new URL(link)

  expect(parsed.username).toBe('synthetic-id')
  expect(parsed.searchParams.get('security')).toBe('reality')
  expect(parsed.searchParams.get('sni')).toBe('edge.example.invalid')
  expect(parsed.searchParams.get('path')).toBe('/ws?x=1')
  expect(parsed.searchParams.has('empty')).toBe(false)
  expect(parsed.hash).toBe('#node%20%2F%20%231')
})

it.each([
  { protocol: 'hysteria2', generate: generateHysteria2URL },
  { protocol: 'anytls', generate: generateAnytlsURL },
])('preserves $protocol query parameters', ({ protocol, generate }) => {
  const link = generate({
    protocol,
    auth: 'synthetic@auth',
    host: 'example.invalid',
    port: 443,
    params: { sni: 'edge.example.invalid', insecure: '0', obfs: 'salamander' },
    hash: 'fixture',
  })
  const parsed = new URL(link)

  expect(parsed.username).toBe('synthetic%40auth')
  expect(parsed.searchParams.get('sni')).toBe('edge.example.invalid')
  expect(parsed.searchParams.get('insecure')).toBe('0')
  expect(parsed.searchParams.get('obfs')).toBe('salamander')
})
