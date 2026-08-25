export function fieldValue(form: FormData, field: string): string {
  const held = form.get(field)
  return typeof held === 'string' ? held.trim() : ''
}

export function numberValue(form: FormData, field: string): number {
  return Number(fieldValue(form, field))
}

export function fieldValues(form: FormData, field: string): string[] {
  return form
    .getAll(field)
    .map((held) => (typeof held === 'string' ? held.trim() : ''))
    .filter((held) => held !== '')
}
