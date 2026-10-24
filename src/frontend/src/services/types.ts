// DTO types mirroring doc/tech/backend-api.md. Field names match the backend
// JSON verbatim (snake_case for admin DTOs, PascalCase for ReleaseInfo-shaped
// payloads that follow upstream Newtonsoft defaults).

// §1.1 server config
export interface ServerConfig {
  notices: string[]
  port: number
  external: boolean
  local_bind_address: string
  cors: boolean
  api_key: string
  blackholedir: string
  updatedisabled: boolean
  prerelease: boolean
  password: string
  logging: boolean
  basepathoverride: string
  baseurloverride: string
  cache_enabled: boolean
  cache_ttl: number
  cache_max_results_per_indexer: number
  flaresolverrurl: string
  flaresolverr_maxtimeout: number
  omdbkey: string
  omdburl: string
  app_version: string
  can_run_netcore: boolean
  proxy_type: string
  proxy_url: string
  proxy_port: number | null
  proxy_username: string
  proxy_password: string
}

// §1.2 POST response adds restart_required
export interface ServerConfigResponse extends ServerConfig {
  restart_required?: boolean
}

// §1.4 logs
export interface CachedLog {
  level: string
  message: string
  when: string
}

// §2.1 indexer DTO
export interface IndexerCapability {
  ID: string
  Name: string
}

export interface IndexerDTO {
  id: string
  name: string
  description: string
  type: string
  configured: boolean
  site_link: string
  alternativesitelinks: string[]
  language: string
  tags: string[]
  last_error: string | null
  potatoenabled: boolean
  caps: IndexerCapability[]
}

// §2.2 dynamic config form description (ConfigurationData.ToJson shape);
// inputtags is emitted in addition to the documented 8 (Tags field).
export type ConfigFieldType =
  | 'inputstring'
  | 'password'
  | 'hiddendata'
  | 'inputbool'
  | 'inputcheckbox'
  | 'inputselect'
  | 'inputtags'
  | 'displayinfo'
  | 'displayimage'

export interface ConfigFieldDef {
  id: string
  type: ConfigFieldType
  name: string
  /** inputbool serializes a JSON boolean; everything else a string. */
  value?: string | boolean
  values?: string[]
  options?: Record<string, string>
}

// §2.3 submitted config items
export interface ConfigItem {
  id: string
  value?: string
  values?: string[]
  cookie?: string
}

// §2.6 / §3.1 release-shaped payloads (PascalCase per upstream serialization)
export interface TrackerCacheResult {
  Title: string
  Guid?: string
  Link?: string
  Details?: string
  PublishDate?: string
  Category?: number[]
  Size?: number
  Seeders?: number
  Peers?: number
  DownloadVolumeFactor?: number
  UploadVolumeFactor?: number
  MinimumRatio?: number | null
  MinimumSeedTime?: number | null
  Imdb?: number | null
  TVDBId?: number | null
  TMDb?: number | null
  Description?: string | null
  Files?: unknown[] | null
  Grabs?: number | null
  Languages?: string[]
  Subs?: string[]
  Genres?: string[]
  Poster?: string
  InfoHash?: string | null
  MagnetUri?: string | null
  FirstSeen?: string
  Tracker?: string
  TrackerId?: string
  TrackerType?: string
  CategoryDesc?: string
  BlackholeLink?: string | null
}

// §3.1 manual search
export interface ManualSearchIndexerState {
  ID: string
  Name: string
  Status: number // 0=Unknown 1=Error 2=OK
  Results: number
  ElapsedTime: number
  Error: string | null
}

export interface ManualSearchResult {
  results: TrackerCacheResult[]
  indexers: ManualSearchIndexerState[]
}
