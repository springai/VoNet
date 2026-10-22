export interface ProxyBindingRoute {
  id: string;
  upstreamProxyId: string;
  priority: number;
}

export interface ProxyBindingGroup {
  id: string;
  deviceId: string;
  iccid: string;
  profileName: string;
  routes: ProxyBindingRoute[];
}
