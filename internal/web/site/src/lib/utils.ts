import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

const typeScale = [
  'label-12',
  'label-13',
  'copy-13',
  'label-14',
  'heading-20',
  'heading-24',
  'heading-32',
  'display-48',
]

const spacingScale = [
  'hair',
  'tight',
  'snug',
  'cell',
  'gutter',
  'section',
  'page',
  'dot',
  'control',
  'head',
  'row',
  'bar',
  'measure',
]

const radiusScale = ['dot', 'control', 'panel', 'frame']

/* Without the named scales registered, a size class reads as a colour class and the merge silently drops it. */
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      'font-size': [{ text: typeScale }],
      p: [{ p: spacingScale }],
      px: [{ px: spacingScale }],
      py: [{ py: spacingScale }],
      pl: [{ pl: spacingScale }],
      pr: [{ pr: spacingScale }],
      gap: [{ gap: spacingScale }],
      w: [{ w: spacingScale }],
      h: [{ h: spacingScale }],
      size: [{ size: spacingScale }],
      'max-w': [{ 'max-w': spacingScale }],
      rounded: [{ rounded: radiusScale }],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
