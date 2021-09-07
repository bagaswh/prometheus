import { configure } from 'enzyme';
import Adapter from '@wojtekmaj/enzyme-adapter-react-17';
import { GlobalWithFetchMock } from 'jest-fetch-mock';
import 'mutationobserver-shim'; // Needed for CodeMirror.
import './globals';
import 'jest-canvas-mock';

configure({ adapter: new Adapter() });
const customGlobal: GlobalWithFetchMock = global as GlobalWithFetchMock;
customGlobal.fetch = require('jest-fetch-mock');
customGlobal.fetchMock = customGlobal.fetch;

// CodeMirror in the expression input requires this DOM API. When we upgrade react-scripts
// and the associated Jest deps, hopefully this won't be needed anymore.
document.getSelection = function () {
  return {
    addRange: function () {
      return;
    },
    removeAllRanges: function () {
      return;
    },
  };
};
