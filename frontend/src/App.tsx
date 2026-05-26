import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { ShelfPage } from './pages/ShelfPage';
import { ReaderPage } from './pages/ReaderPage';
import './App.css';

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<ShelfPage />} />
        <Route path="/read/:id" element={<ReaderPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
