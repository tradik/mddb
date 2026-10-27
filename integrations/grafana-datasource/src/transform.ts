import { FieldType, createDataFrame, type DataFrame } from '@grafana/data';
import type {
  AggregateResponse,
  FtsResponse,
  MddbQuery,
  StatsResponse,
  TemporalHistogramResponse,
  TemporalHotResponse,
} from './types';

/** Convert a MDDB response payload into a Grafana DataFrame, dispatching on query type. */
export function toDataFrame(query: MddbQuery, payload: unknown): DataFrame {
  switch (query.queryType) {
    case 'temporal-histogram':
      return histogramFrame(query.refId, payload as TemporalHistogramResponse);
    case 'temporal-hot':
      return hotFrame(query.refId, payload as TemporalHotResponse);
    case 'aggregate':
      return aggregateFrame(query.refId, payload as AggregateResponse);
    case 'fts':
      return ftsFrame(query.refId, payload as FtsResponse);
    case 'stats':
      return statsFrame(query.refId, payload as StatsResponse);
    default:
      return createDataFrame({ refId: query.refId, fields: [] });
  }
}

function histogramFrame(refId: string, payload: TemporalHistogramResponse): DataFrame {
  const buckets = payload?.buckets ?? [];
  const times: number[] = [];
  const counts: number[] = [];
  for (const b of buckets) {
    if (typeof b.from !== 'number' || typeof b.count !== 'number') continue;
    times.push(b.from * 1000);
    counts.push(b.count);
  }
  return createDataFrame({
    refId,
    name: `${payload?.collection ?? 'mddb'} / ${payload?.eventType ?? 'access'}`,
    fields: [
      { name: 'time', type: FieldType.time, values: times },
      { name: 'count', type: FieldType.number, values: counts },
    ],
  });
}

function hotFrame(refId: string, payload: TemporalHotResponse): DataFrame {
  const entries = payload?.entries ?? [];
  return createDataFrame({
    refId,
    name: `${payload?.collection ?? 'mddb'} / hot`,
    fields: [
      { name: 'docId', type: FieldType.string, values: entries.map((e) => e.docId ?? '') },
      {
        name: 'accessCount',
        type: FieldType.number,
        values: entries.map((e) => e.accessCount ?? 0),
      },
      {
        name: 'lastAccessAt',
        type: FieldType.time,
        values: entries.map((e) => (e.lastAccessAt ?? 0) * 1000),
      },
    ],
  });
}

function aggregateFrame(refId: string, payload: AggregateResponse): DataFrame {
  if (payload?.buckets && payload.buckets.length > 0) {
    return createDataFrame({
      refId,
      name: `${payload?.collection ?? 'mddb'} / ${payload?.key ?? 'agg'}`,
      fields: [
        {
          name: 'time',
          type: FieldType.time,
          values: payload.buckets.map((b) => (b.from ?? 0) * 1000),
        },
        {
          name: 'count',
          type: FieldType.number,
          values: payload.buckets.map((b) => b.count ?? 0),
        },
      ],
    });
  }
  const values = payload?.values ?? [];
  return createDataFrame({
    refId,
    name: `${payload?.collection ?? 'mddb'} / ${payload?.key ?? 'agg'}`,
    fields: [
      { name: 'value', type: FieldType.string, values: values.map((v) => v.value ?? '') },
      { name: 'count', type: FieldType.number, values: values.map((v) => v.count ?? 0) },
    ],
  });
}

function ftsFrame(refId: string, payload: FtsResponse): DataFrame {
  const results = payload?.results ?? [];
  return createDataFrame({
    refId,
    name: 'mddb / fts',
    fields: [
      { name: 'key', type: FieldType.string, values: results.map((r) => r.key ?? '') },
      { name: 'lang', type: FieldType.string, values: results.map((r) => r.lang ?? '') },
      { name: 'score', type: FieldType.number, values: results.map((r) => r.score ?? 0) },
      {
        name: 'highlight',
        type: FieldType.string,
        values: results.map((r) => (r.highlights ?? []).join(' … ')),
      },
    ],
  });
}

function statsFrame(refId: string, payload: StatsResponse): DataFrame {
  const collections = payload?.collections ?? {};
  const names = Object.keys(collections).sort((a, b) => a.localeCompare(b));
  return createDataFrame({
    refId,
    name: 'mddb / stats',
    fields: [
      { name: 'collection', type: FieldType.string, values: names },
      {
        name: 'documents',
        type: FieldType.number,
        values: names.map((n) => collections[n]?.documents ?? 0),
      },
      {
        name: 'revisions',
        type: FieldType.number,
        values: names.map((n) => collections[n]?.revisions ?? 0),
      },
      {
        name: 'embeddings',
        type: FieldType.number,
        values: names.map((n) => payload?.vectorEmbeddings?.[n] ?? 0),
      },
    ],
  });
}
