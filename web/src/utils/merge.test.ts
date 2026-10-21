import { describe, expect, it } from 'vitest'
import { mergeBySeq } from './merge'

describe('mergeBySeq', () => {
  it('deduplicates and sorts by sequence', () => {
    expect(
      mergeBySeq(
        [{ seq: 2, text: 'old' }, { seq: 1, text: 'one' }],
        [{ seq: 2, text: 'new' }, { seq: 3, text: 'three' }],
      ),
    ).toEqual([
      { seq: 1, text: 'one' },
      { seq: 2, text: 'new' },
      { seq: 3, text: 'three' },
    ])
  })

  it('keeps the newest items when capped', () => {
    expect(mergeBySeq([{ seq: 1 }, { seq: 2 }], [{ seq: 3 }], 2)).toEqual([{ seq: 2 }, { seq: 3 }])
  })
})
