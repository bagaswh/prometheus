import React, { ChangeEvent, FC, useEffect, useState } from 'react';
import { useFetch } from '../../hooks/useFetch';
import { LabelsTable } from './LabelsTable';
import { DroppedTarget, Labels, Target } from '../targets/target';

import { withStatusIndicator } from '../../components/withStatusIndicator';
import { mapObjEntries } from '../../utils';
import { usePathPrefix } from '../../contexts/PathPrefixContext';
import { API_PATH } from '../../constants/constants';
import { KVSearch } from '@nexucis/kvsearch';
import { Container } from 'reactstrap';
import SearchBar from '../../components/SearchBar';

interface ServiceMap {
  activeTargets: Target[];
  droppedTargets: DroppedTarget[];
}

export interface TargetLabels {
  discoveredLabels: Labels;
  labels: Labels;
  isDropped: boolean;
}

const kvSearch = new KVSearch({
  shouldSort: true,
  indexedKeys: ['labels', 'discoveredLabels', ['discoveredLabels', /.*/], ['labels', /.*/]],
});

export const processSummary = (
  activeTargets: Target[],
  droppedTargets: DroppedTarget[]
): Record<string, { active: number; total: number }> => {
  const targets: Record<string, { active: number; total: number }> = {};

  // Get targets of each type along with the total and active end points
  for (const target of activeTargets) {
    const { scrapePool: name } = target;
    if (!targets[name]) {
      targets[name] = {
        total: 0,
        active: 0,
      };
    }
    targets[name].total++;
    targets[name].active++;
  }
  for (const target of droppedTargets) {
    const { job: name } = target.discoveredLabels;
    if (!targets[name]) {
      targets[name] = {
        total: 0,
        active: 0,
      };
    }
    targets[name].total++;
  }

  return targets;
};

export const processTargets = (activeTargets: Target[], droppedTargets: DroppedTarget[]): Record<string, TargetLabels[]> => {
  const labels: Record<string, TargetLabels[]> = {};

  for (const target of activeTargets) {
    const name = target.scrapePool;
    if (!labels[name]) {
      labels[name] = [];
    }
    labels[name].push({
      discoveredLabels: target.discoveredLabels,
      labels: target.labels,
      isDropped: false,
    });
  }

  for (const target of droppedTargets) {
    const { job: name } = target.discoveredLabels;
    if (!labels[name]) {
      labels[name] = [];
    }
    labels[name].push({
      discoveredLabels: target.discoveredLabels,
      isDropped: true,
      labels: {},
    });
  }

  return labels;
};

export const ServiceDiscoveryContent: FC<ServiceMap> = ({ activeTargets, droppedTargets }) => {
  const [activeTargetList, setActiveTargetList] = useState(activeTargets);
  const [targetList, setTargetList] = useState(processSummary(activeTargets, droppedTargets));
  const [labelList, setLabelList] = useState(processTargets(activeTargets, droppedTargets));

  const handleSearchChange = (e: ChangeEvent<HTMLTextAreaElement | HTMLInputElement>) => {
    if (e.target.value !== '') {
      const result = kvSearch.filter(e.target.value.trim(), activeTargets);
      setActiveTargetList(
        result.map((value) => {
          return value.original as unknown as Target;
        })
      );
    } else {
      setActiveTargetList(activeTargets);
    }
  };

  useEffect(() => {
    setTargetList(processSummary(activeTargetList, droppedTargets));
    setLabelList(processTargets(activeTargetList, droppedTargets));
  }, [activeTargetList, droppedTargets]);

  return (
    <>
      <h2>Service Discovery</h2>
      <Container>
        <SearchBar handleChange={handleSearchChange} placeholder="filter by labels" />
      </Container>
      <ul>
        {mapObjEntries(targetList, ([k, v]) => (
          <li key={k}>
            <a href={'#' + k}>
              {k} ({v.active} / {v.total} active targets)
            </a>
          </li>
        ))}
      </ul>
      <hr />
      {mapObjEntries(labelList, ([k, v]) => {
        return <LabelsTable value={v} name={k} key={k} />;
      })}
    </>
  );
};
ServiceDiscoveryContent.displayName = 'ServiceDiscoveryContent';

const ServicesWithStatusIndicator = withStatusIndicator(ServiceDiscoveryContent);

const ServiceDiscovery: FC = () => {
  const pathPrefix = usePathPrefix();
  const { response, error, isLoading } = useFetch<ServiceMap>(`${pathPrefix}/${API_PATH}/targets`);
  return (
    <ServicesWithStatusIndicator
      {...response.data}
      error={error}
      isLoading={isLoading}
      componentTitle="Service Discovery information"
    />
  );
};

export default ServiceDiscovery;
