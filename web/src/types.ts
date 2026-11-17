export type ApiStatus = "ok" | "error";

export type DeviceType = "wifi_410" | "dji_4g" | "ml307" | "pcie_ec20_ec25" | "usb_sim_reader" | "zxic";

export interface Session {
  authenticated: boolean;
  username: string;
  role: string;
  expiresAt?: string;
  csrfToken?: string;
}

export interface LoginResponse {
  status: ApiStatus;
  authenticated?: boolean;
  username?: string;
  role?: string;
  expiresAt?: string;
  csrfToken?: string;
  user?: {
    username?: string;
    role?: string;
  };
}

export interface ApiErrorBody {
  status?: string;
  code?: string;
  error?: string;
  message?: string;
  requestId?: string;
  warning?: string;
  busy?: boolean;
}

export interface CellLockTarget {
  earfcn: number;
  pci: number;
}

export interface CellLockStatus {
  target: CellLockTarget | null;
}

export interface CellInfo extends CellLockTarget {
  plmn: string;
  source: "serving" | "neighbor";
  rsrp?: number;
  rsrq?: number;
  rssi?: number;
  sinr?: number;
}

export interface DeviceCells {
  items: CellInfo[];
  neighborsStatus: "available" | "unsupported" | "unavailable";
}

export interface VoWiFiRuntime {
  deviceId: string;
  phase: string;
  enabled?: boolean;
  active?: boolean;
  carrierProfile?: string;
  carrierProfileFrom?: string;
  dataplaneMode: string;
  iccid: string;
  imsi: string;
  simReady: boolean;
  accessReady: boolean;
  tunnelReady: boolean;
  imsReady: boolean;
  smsReady: boolean;
  regStatus: number;
  regStatusText: string;
  networkMode: string;
  localPhone?: string;
  phoneNumberSource?: string;
  lastErrorClass: string;
  lastError: string;
  lastReason: string;
  updatedAt: string;
  tunnel?: Record<string, unknown>;
  imscore?: Record<string, unknown>;
  smsip?: Record<string, unknown>;
}

export interface ModemSummary {
  operator: string;
  nativeMcc: string;
  nativeMnc: string;
  operatorCountryCode?: string;
  nativeSpn?: string;
  cardMcc?: string;
  cardMnc?: string;
  cardCountry?: string;
  homeCarrierName?: string;
  homeCarrierPlmn?: string;
  homeCarrierCountryCode?: string;
  serviceBlocked?: boolean;
  blockedReason?: string;
  networkMode: string;
  networkDuplex?: string;
  radioBand: string;
  radioChannel: number;
  signalDbm: number;
  signalRsrp?: number;
  signalRsrq?: number;
  signalSinr: number;
  imei: string;
  iccid: string;
  imsi?: string;
  firmware?: string;
  model?: string;
  regStatus: number;
  regStatusText?: string;
  psAttached?: boolean;
  simInserted?: boolean;
  operatingMode?: number;
  phoneNumber?: string;
  phoneNumberSource?: string;
}

export interface PublicIPInfo {
  detected?: boolean;
  ip: string;
  countryCode: string;
  region?: string;
  city?: string;
  organization?: string;
}

export interface SMSStorageArea {
  used?: number;
  total?: number;
}

export interface SMSStorageUsage {
  sm?: SMSStorageArea;
  me?: SMSStorageArea;
}

export interface SMSSettings {
  autoClearModemStorage: boolean;
}

export interface DeviceListItem {
  id: string;
  name: string;
  deviceType: DeviceType;
  supportsCellLock: boolean;
  running: boolean;
  healthy: boolean;
  controlOnline: boolean;
  physicalPresent?: boolean;
  workerRunning?: boolean;
  dataConnected?: boolean;
  radioRegistered?: boolean;
  lifecyclePhase?: string;
  lifecycleReason?: string;
  publicIp: string;
  privateIp?: string;
  interface: string;
  esimTransport: string;
  smsEnabled: boolean;
  smsStorage?: SMSStorageUsage;
	  networkEnabled: boolean;
  vowifiEnabled: boolean;
  vowifiActive?: boolean;
  vowifiRuntime: VoWiFiRuntime;
  modem: ModemSummary;
  networkConnected: boolean;
	  networkPhase?: "unknown" | "starting" | "connected" | "stopping" | "recovering" | "disabled" | "failed";
	  networkError?: string;
	  modemPhase?: "rebooting" | "";
	  publicIpInfo?: PublicIPInfo;
  registrationStateLabel: "registered" | "searching" | "denied" | "unknown";
  flightMode?: boolean;
}

export interface DevicesResponse {
  devices: DeviceListItem[];
}

export interface DashboardDevice {
  id: string;
  name: string;
  deviceType: DeviceType;
  interface: string;
  proxyPort: number;
  publicIp: string;
  healthy: boolean;
  operator: string;
  signalDbm: number;
  networkMode: string;
  networkDuplex?: string;
  vowifiActive: boolean;
  vowifiRuntime: VoWiFiRuntime;
  networkConnected: boolean;
  model?: string;
}

export interface DashboardHostInfo {
  cpuModel: string;
  boardModel: string;
  memoryModel: string;
  diskModel: string;
}

export interface DashboardHostPerf {
  cpuPercent: number;
  memoryPercent: number;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
  diskPercent: number;
  diskUsedBytes: number;
  diskTotalBytes: number;
  netRxBps: number;
  netTxBps: number;
}

export interface DashboardHost {
  host: DashboardHostInfo;
  perf: DashboardHostPerf;
}

// 仪表盘定时任务卡只关心名字与下次执行时间。
export interface DashboardUpcomingTask {
  id: number;
  name: string;
  enabled: boolean;
  nextRunAt: string;
}

export interface DeviceOverview extends DeviceListItem {
  atPort?: string;
  audioDevice?: string;
  backendMode?: string;
  controlDevice?: string;
  radioLiveOk?: boolean | null;
  traffic?: Record<string, string>;
  trafficRaw?: Record<string, number>;
  trafficMeta?: Record<string, unknown>;
}

export interface DeviceStatus {
  id: string;
  name: string;
  healthy: boolean;
  interface: string;
  publicIp: string;
  proxyPort: number;
  lastHardwareRefresh?: string;
  networkConnected: boolean;
  modem: ModemSummary;
  vowifi?: Record<string, unknown>;
  simServiceTable?: Record<string, unknown>;
  pnn?: Array<Record<string, unknown>>;
  opl?: Array<Record<string, unknown>>;
}

export interface DiscoveredDevice {
  agentId: string;
  remoteDeviceId: string;
  hardwareGeneration?: string;
	 hardwareKind?: string;
	 readerName?: string;
  deviceType?: DeviceType;
  discoveryKey: string;
  controlPath: string;
  netInterface: string;
  usbPath: string;
  vendorId: number;
  productId: number;
  driverName: string;
  atPorts: string[];
  atPort: string;
  audioDevice?: string;
  imei?: string;
  mode: string;
  networkCapable: boolean;
  configured: boolean;
  configuredId?: string;
  degraded?: boolean;
  discoveryIssue?: "pcsc_service_unavailable" | "pcsc_driver_missing" | string;
  usbnetMode?: number | null;
  suggested_id?: string;
}

export interface DeviceConfig {
  id: string;
  agentId?: string;
  remoteDeviceId?: string;
  remoteHardwareGeneration?: string;
  name: string;
  deviceType: DeviceType;
  interface: string;
  controlDevice: string;
  atPort: string;
  usbPath: string;
  audioDevice?: string;
  modemImei?: string;
  pcscImeiMode?: "random" | "fixed";
  pcscImei?: string;
	 simPin?: string;
  apn: string;
  proxyPort: number;
  baudRate: number;
  dataBits: number;
  stopBits: number;
  parity: string;
  deviceBackend: "at" | "qmi" | "pcsc";
  esimTransport: "at" | "qmi" | "pcsc" | "none";
  qmiUseProxy: boolean;
  qmiProxyPath?: string;
  qmiProxyExecutable?: string;
  smsEnabled: boolean;
  vowifiEnabled: boolean;
}

export interface USBNetMode {
  mode: number;
  name: string;
  rebootRequired?: boolean;
}

export interface OperatorSelection {
  mode: number;
  format: number;
  operator: string;
  accessTechnology?: string;
}

export type SimCardType = "physical_sim" | "esim_profile";
export type SimCardStatus = "active" | "offline" | "conflict" | "unknown";
export type CardDeviceMode = "at" | "qmi" | "mbim" | "pcsc";
export type CellularIMSMode = "mbn_default" | "force_enabled" | "force_disabled";

export interface SimCardBinding {
  deviceId?: string;
  deviceName?: string;
  deviceType?: DeviceType;
  backendMode?: string;
  atPort?: string;
  running?: boolean;
  online?: boolean;
  physicalDeviceId?: string;
  eid?: string;
  profileAid?: string;
  boundAt?: string;
  lastSeenAt?: string;
}

export interface SimCard {
  iccid: string;
  displayName?: string;
  notes?: string;
  cardType: SimCardType;
  hostType?: DeviceType;
  status: SimCardStatus;
  policyStatus?: "applied" | "pending" | "waiting" | "suspended" | "offline" | "failed" | "missing";
  policy?: CardPolicy;
  registered: boolean;
  conflict?: boolean;
  imsi?: string;
  msisdn?: string;
  carrier?: string;
  spn?: string;
  mcc?: string;
  mnc?: string;
  homePlmn?: string;
  eid?: string;
  profileAid?: string;
  binding?: SimCardBinding;
  bindings?: SimCardBinding[];
  lastSeenAt?: string;
  lastActiveAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface SimCardInput {
  iccid: string;
  displayName?: string;
  notes?: string;
  cardType: SimCardType;
  hostType?: DeviceType;
  imsi?: string;
  msisdn?: string;
  carrier?: string;
  spn?: string;
  mcc?: string;
  mnc?: string;
  homePlmn?: string;
  eid?: string;
  profileAid?: string;
  deviceId?: string;
}

export interface SimCardCandidate {
  iccid: string;
  deviceId: string;
  physicalDeviceId?: string;
  deviceName?: string;
  hostType?: DeviceType;
  cardType: SimCardType;
  displayName?: string;
  imsi?: string;
  msisdn?: string;
  carrier?: string;
  spn?: string;
  mcc?: string;
  mnc?: string;
  eid?: string;
  profileAid?: string;
  state: "pending" | "stable" | "offline";
  conflict?: boolean;
  registered?: boolean;
  firstSeenAt?: string;
  lastSeenAt?: string;
}

export interface SimCardsResponse {
  cards: SimCard[];
}

export interface CardPolicy {
  iccid: string;
  vowifiEnabled: boolean;
  airplaneEnabled: boolean;
  networkEnabled?: boolean;
  runtimeMode?: CardDeviceMode;
  cellularImsMode?: CellularIMSMode;
  apn?: string;
  ipVersion?: string;
  customPhoneNumber?: string;
  mbnProfile?: string;
  source?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface SMSContact {
  deviceId: string;
  deviceName?: string;
  modemImei?: string;
  iccid?: string;
  imsi: string;
  localPhone?: string;
  peer: string;
  displayName?: string;
  lastMessage?: string;
  lastContent?: string;
  lastTimestamp: string;
  direction?: string;
  lastType?: string;
  lastSmsId?: number;
  unreadCount: number;
  messageCount?: number;
}

export interface SMSMessage {
  id: number;
  messageId?: string;
  deviceId: string;
  modemImei?: string;
  iccid?: string;
  imsi: string;
  peer: string;
  direction: "inbound" | "outbound" | "received" | "sent";
  body?: string;
  decodeStatus?: "decoded" | "empty" | "failed";
  decodeError?: string;
  content?: string;
  sender?: string;
  recipient?: string;
  localPhone?: string;
  deviceName?: string;
  type?: string;
  timestamp: string;
  status: string;
  source?: string;
  deliveryState?: string;
  serviceCenterTimestamp?: string;
  serviceCenterTimestampCorrection?: {
    originalTimestamp: string;
    timezone: string;
    carrierProfile: string;
    reason: string;
  };
}

export interface UpstreamProxy {
  id: string;
  name: string;
  addr: string;
  username: string;
  password?: string;
  enabled: boolean;
}

export interface UpstreamProxyProbe {
  reachable?: boolean;
  handshakeOk?: boolean;
  udpAssociateOk?: boolean;
  udpExchangeOk?: boolean;
  authMethod?: string;
  relayAddr?: string;
  dnsServer?: string;
  dnsName?: string;
  dnsRcode?: number;
  roundTripMs?: number;
  diagnosis?: string;
  hint?: string;
  error?: string;
}

export interface UpstreamProxySaveResponse {
  status?: string;
  proxy?: UpstreamProxy;
  probe?: UpstreamProxyProbe;
  message?: string;
}

export interface Country {
  countryCode: string;
  countryName: string;
  mccs: string[];
}

export interface CountryRule {
  countryCode: string;
  countryName?: string;
  upstreamProxyId: string;
  enabled: boolean;
}

export interface DeviceProxyBinding {
  deviceId: string;
  iccid: string;
  profileName: string;
  upstreamProxyId: string;
  reconnectRequested?: boolean;
  reconnectError?: string;
}

export interface ProfileProxyCandidate {
  deviceId: string;
  iccid: string;
  profileName: string;
  stateText?: string;
}

export interface LogEntry {
  time: string;
  level: "debug" | "info" | "warn" | "error" | string;
  message: string;
  caller?: string;
  fields?: string | Record<string, unknown>;
}

export interface EsimProfile {
  iccid: string;
  name: string;
  serviceProviderName: string;
  state: number;
  stateText: string;
  classText?: string;
}

export interface EsimEuiccProfiles {
  eid: string;
  aidHex: string;
  profiles: EsimProfile[];
}

export interface EsimOverview {
  available?: boolean;
  reason?: string;
  stale?: boolean;
  cachedAt?: number;
  chipInfo?: {
    serialNumber?: string;
    skuName?: string;
    firmware?: string;
    eids?: Array<Record<string, unknown>>;
  };
  profiles: EsimEuiccProfiles[];
}

export type NotificationConfig<T = Record<string, unknown>> = T & {
  deviceIds?: string[] | null; // Missing in older settings; null means all devices.
};

export interface ApiTokenListItem {
  name: string;
  readOnly: boolean;
  createdAt: string;
  lastUsedAt: string;
  expiresAt: string;
}

export interface NotificationSettings {
  meow: NotificationConfig<{ enabled: boolean; nickname: string; url: string; imgUrl: string }>;
  qqbot: NotificationConfig<{
    enabled: boolean;
    appId: string;
    appSecret: string;
    targetOpenid: string;
    proxy: string;
  }>;
  telegram: NotificationConfig;
  webhook: NotificationConfig;
  bark: NotificationConfig;
  email: NotificationConfig;
  pushplus: NotificationConfig;
  wecom: NotificationConfig;
  lark: NotificationConfig;
}

// 网络访问控制策略：默认仅放行内网网段，可切换到对公网开放。
export interface SecuritySettings {
  mode: "internal" | "public";
  allowedCidrs: string[];
  trustProxyHeaders: boolean;
  clientIp: string;
  clientAllowed: boolean;
}

// 运行日志保留策略：全局硬上限 10000 条，可配置更严格的条数或天数限制。
export interface LoggingSettings {
  mode: "unlimited" | "count" | "days";
  count: number;
  days: number;
  level: "debug" | "info" | "warn" | "error";
  storedLogs: number;
  maxLogs: number;
}

export interface SystemInfo {
  version: string;
  buildTime: string;
  config: string;
  os?: string;
  architecture?: string;
  uptime?: string;
}

export interface HTTPSSettings {
  enabled: boolean;
  httpUrl: string;
  httpsUrl: string;
  certificateSource?: "self_signed" | "uploaded";
  certificateSubject?: string;
  fingerprint?: string;
  notAfter?: string;
  restartPending?: boolean;
}

export interface SystemLimitsSettings {
  smsHourlyLimit: number;
  defaultSmsHourlyLimit: number;
  autoClearModemStorage?: boolean;
}

export interface VoNetAgent {
  id: string;
  name: string;
  notes?: string;
  keyVersion: number;
  status: "waiting_for_key" | "online" | "offline" | "revoked";
  certificateFingerprint?: string;
  protocolVersion?: string;
  createdAt?: string;
  updatedAt?: string;
  lastSeenAt?: string;
}

export type Notice = {
  kind: "success" | "error" | "warning" | "info";
  title: string;
  detail?: string;
} | null;

export type SSOProviderKind = "pocketid" | "github" | "generic";

export interface SSOConfigInput {
  enabled: boolean;
  kind: SSOProviderKind;
  displayName: string;
  publicOrigin: string;
  clientId: string;
  issuerUrl: string;
  authUrl: string;
  tokenUrl: string;
  userInfoUrl: string;
  userIdField: string;
  scopes: string[];
  tokenAuthMethod: "auto" | "client_secret_basic" | "client_secret_post";
  pkceMode: "s256" | "disabled";
}

export interface SSOConfig extends SSOConfigInput {
  revision: number;
}

export interface SSOBinding {
  bound: boolean;
  displayName: string;
  email: string;
  createdAt: string | null;
  lastLoginAt: string | null;
}

export interface SSOSettings {
  config: SSOConfig;
  binding: SSOBinding;
  configured: boolean;
  hasClientSecret: boolean;
  reauthenticationRequired?: boolean;
}

export interface SSOSaveRequest {
  expectedRevision: number;
  config: SSOConfigInput;
  clientSecret?: string;
  resetBinding?: boolean;
}

export interface SSOProviders {
  sso: { enabled: boolean; displayName?: string };
}

export interface SSOCheckResult {
  revision: number;
}

export interface SSOUnbindResult {
  reauthenticationRequired: boolean;
}
