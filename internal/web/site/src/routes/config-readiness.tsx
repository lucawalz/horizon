import type { ProviderConfigSummary } from '@/lib/api'
import { ConditionChip } from '@/routes/chips'
import { Definition, DefinitionGrid, Panel } from '@/routes/page'
import { absent } from '@/routes/units'

const readyCondition = 'Ready'
const catalogueCondition = 'CataloguePublished'

export function ConfigReadiness({ config }: { config: ProviderConfigSummary }) {
  return (
    <Panel title="Readiness">
      <DefinitionGrid>
        <Definition label="Ready">
          <ConditionChip type={readyCondition} status={config.ready} />
        </Definition>
        <Definition label="Catalogue published">
          <ConditionChip type={catalogueCondition} status={config.cataloguePublished} />
        </Definition>
        <Definition label="Reason">{config.reason ?? absent}</Definition>
      </DefinitionGrid>
      {config.message === null ? null : (
        <p className="border-t border-line px-gutter py-cell text-copy-13 text-subtle">
          {config.message}
        </p>
      )}
    </Panel>
  )
}
