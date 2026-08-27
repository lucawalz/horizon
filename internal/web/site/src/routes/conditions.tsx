import {
  Cell,
  HeadCell,
  Row,
  Table,
  TableBody,
  TableEmpty,
  TableHead,
} from '@/components/data-table'
import type { ConditionEntry } from '@/lib/api'
import { ConditionChip, Since } from '@/routes/chips'
import { absent } from '@/routes/units'

const conditionColumns = 5

export function ConditionsTable({
  conditions,
  empty,
}: {
  conditions: ConditionEntry[]
  empty: string
}) {
  return (
    <Table>
      <TableHead>
        <Row>
          <HeadCell>Condition</HeadCell>
          <HeadCell>Status</HeadCell>
          <HeadCell>Reason</HeadCell>
          <HeadCell>Message</HeadCell>
          <HeadCell numeric>Changed</HeadCell>
        </Row>
      </TableHead>
      <TableBody>
        {conditions.length === 0 ? (
          <TableEmpty span={conditionColumns}>{empty}</TableEmpty>
        ) : (
          conditions.map((condition) => (
            <Row key={condition.type}>
              <Cell className="font-emphasis text-ink-strong">{condition.type}</Cell>
              <Cell>
                <ConditionChip type={condition.type} status={condition.status} />
              </Cell>
              <Cell muted>{condition.reason ?? absent}</Cell>
              <Cell muted>
                <span
                  className="block max-w-[40ch] break-words"
                  title={condition.message ?? undefined}
                >
                  {condition.message ?? absent}
                </span>
              </Cell>
              <Cell numeric muted>
                <Since at={condition.lastTransitionTime} />
              </Cell>
            </Row>
          ))
        )}
      </TableBody>
    </Table>
  )
}
