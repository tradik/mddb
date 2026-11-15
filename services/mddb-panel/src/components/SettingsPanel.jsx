import { Settings, Radio, CheckCircle } from 'lucide-react';
import { useStore } from '../lib/store';

export default function SettingsPanel() {
  const { apiMode, setApiMode } = useStore();

  const apiModes = [
    {
      id: 'rest',
      name: 'REST API',
      description: 'Traditional HTTP/JSON API - default, stable, full feature support',
      icon: Radio,
      features: [
        'Streaming responses (export, backup)',
        'Simple curl/wget access',
        'Well-tested and stable',
        'Full feature support',
      ],
    },
    {
      id: 'graphql',
      name: 'GraphQL API',
      description: 'Modern GraphQL API - flexible queries, type-safe',
      icon: Settings,
      features: [
        'Flexible field selection',
        'Combine multiple queries',
        'Type-safe schema',
        'Interactive Playground',
      ],
      note: 'GraphQL must be enabled on server (MDDB_GRAPHQL_ENABLED=true)',
    },
  ];

  const handleModeChange = (mode) => {
    setApiMode(mode);
  };

  return (
    <div className="p-6">
      <div className="mb-6">
        <h2 className="text-2xl font-bold text-fg flex items-center">
          <Settings className="w-6 h-6 mr-2" />
          Client Settings
        </h2>
        <p className="text-sm text-fg-subtle mt-1">
          Configure how the panel communicates with the MDDB server
        </p>
      </div>

      <div className="bg-surface rounded-lg shadow-sm border border-line">
        <div className="p-4 border-b border-line">
          <h3 className="text-lg font-semibold text-fg">API Mode</h3>
          <p className="text-sm text-fg-subtle mt-1">
            Choose which API protocol to use for communication
          </p>
        </div>

        <div className="p-4 space-y-4">
          {apiModes.map((mode) => {
            const Icon = mode.icon;
            const isSelected = apiMode === mode.id;

            return (
              <div
                key={mode.id}
                onClick={() => handleModeChange(mode.id)}
                className={`
                  relative p-4 rounded-lg border-2 cursor-pointer transition-all
                  ${isSelected
                    ? 'border-blue-500 bg-info-soft'
                    : 'border-line hover:border-line-strong bg-surface'
                  }
                `}
              >
                <div className="flex items-start">
                  <div
                    className={`
                    flex-shrink-0 w-10 h-10 rounded-full flex items-center justify-center
                    ${isSelected ? 'bg-info-muted' : 'bg-surface-subtle'}
                  `}
                  >
                    <Icon
                      className={`w-5 h-5 ${isSelected ? 'text-info' : 'text-fg-subtle'}`}
                    />
                  </div>

                  <div className="ml-4 flex-1">
                    <div className="flex items-center justify-between">
                      <h4 className="text-base font-semibold text-fg">{mode.name}</h4>
                      {isSelected && (
                        <CheckCircle className="w-5 h-5 text-info" />
                      )}
                    </div>

                    <p className="text-sm text-fg-subtle mt-1">{mode.description}</p>

                    <ul className="mt-3 space-y-1">
                      {mode.features.map((feature, idx) => (
                        <li
                          key={idx}
                          className="text-xs text-fg-subtle flex items-center"
                        >
                          <span className="w-1 h-1 bg-line-strong rounded-full mr-2"></span>
                          {feature}
                        </li>
                      ))}
                    </ul>

                    {mode.note && (
                      <div className="mt-3 p-2 bg-warning-soft border border-warning-line rounded text-xs text-warning-strong">
                        <strong>Note:</strong> {mode.note}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <div className="p-4 bg-surface-muted border-t border-line">
          <div className="flex items-start">
            <div className="flex-shrink-0">
              <Settings className="w-5 h-5 text-info" />
            </div>
            <div className="ml-3">
              <h4 className="text-sm font-semibold text-fg">Current Mode</h4>
              <p className="text-xs text-fg-subtle mt-1">
                Selected: <strong className="text-fg">{apiMode.toUpperCase()}</strong>.
              </p>
              {apiMode === 'graphql' && (
                <p className="text-xs text-warning mt-2">
                  ⚠️ The MDDB server now serves a fully-functional GraphQL endpoint at
                  <code> /graphql</code> (and Playground at <code>/playground</code>) — enabled by
                  default in MDDB 2.9.14+. The panel UI itself, however, still issues every
                  request through the REST client; full panel routing through GraphQL is
                  scheduled for a follow-up release. Use this toggle as a preference marker
                  for now and call <code>/graphql</code> directly from your own GraphQL client
                  (Apollo, urql, curl) to take advantage of the new resolvers.
                </p>
              )}
            </div>
          </div>
        </div>
      </div>

      <div className="mt-6 bg-info-soft border border-info-line rounded-lg p-4">
        <h4 className="text-sm font-semibold text-info-strong mb-2">💡 Tips</h4>
        <ul className="space-y-1 text-xs text-info-strong">
          <li className="flex items-start">
            <span className="mr-2">•</span>
            <span>
              <strong>REST API</strong> is the default and recommended for most use cases
            </span>
          </li>
          <li className="flex items-start">
            <span className="mr-2">•</span>
            <span>
              <strong>GraphQL API</strong> is ideal for complex queries and modern frontends
            </span>
          </li>
          <li className="flex items-start">
            <span className="mr-2">•</span>
            <span>
              Test GraphQL queries at{' '}
              <code className="bg-info-muted px-1 rounded">/playground</code>
            </span>
          </li>
          <li className="flex items-start">
            <span className="mr-2">•</span>
            <span>The setting is saved in your browser's localStorage</span>
          </li>
        </ul>
      </div>
    </div>
  );
}
