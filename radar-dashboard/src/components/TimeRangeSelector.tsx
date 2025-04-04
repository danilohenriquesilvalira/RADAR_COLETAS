import { TimeRange } from './Dashboard';

interface TimeRangeSelectorProps {
  currentRange: TimeRange;
  onRangeChange: (range: TimeRange) => void;
}

const TimeRangeSelector = ({ currentRange, onRangeChange }: TimeRangeSelectorProps) => {
  const timeRanges: { value: TimeRange; label: string }[] = [
    { value: '1m', label: '1 min' },
    { value: '5m', label: '5 min' },
    { value: '15m', label: '15 min' },
    { value: '30m', label: '30 min' },
    { value: '1h', label: '1 hora' },
    { value: '6h', label: '6 horas' },
    { value: '12h', label: '12 horas' },
    { value: '24h', label: '24 horas' },
    { value: 'all', label: 'Tudo' },
  ];

  return (
    <div className="bg-white p-3 rounded-lg shadow flex flex-wrap items-center gap-2">
      <span className="text-sm font-medium text-gray-700 mr-2">Período:</span>
      <div className="flex flex-wrap gap-1">
        {timeRanges.map((range) => (
          <button
            key={range.value}
            onClick={() => onRangeChange(range.value)}
            className={`px-3 py-1 text-xs rounded-full transition-colors ${
              currentRange === range.value
                ? 'bg-blue-600 text-white'
                : 'bg-gray-100 text-gray-800 hover:bg-gray-200'
            }`}
          >
            {range.label}
          </button>
        ))}
      </div>
    </div>
  );
};

export default TimeRangeSelector;