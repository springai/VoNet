export interface EsimActionAvailability {
  deviceOnline: boolean;
  profilesStale: boolean;
}

export function esimMutationDisabled(state: EsimActionAvailability) {
  return state.profilesStale || !state.deviceOnline;
}

export function esimDownloadDisabled(state: EsimActionAvailability, downloading: boolean) {
  return downloading || esimMutationDisabled(state);
}

export function createEsimMutationGuard(readState: () => EsimActionAvailability) {
  return () => esimMutationDisabled(readState());
}
